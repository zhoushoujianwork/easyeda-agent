package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// planApplyLive backs planApplyBoard with typed actions. Every read checks that
// the window still shows the expected project and a PCB, so a window that moved
// on (app restart, tab switch) stops the run at the next step instead of
// silently failing hundreds of writes (#310).
type planApplyLive struct {
	cfg         *appConfig
	window      string
	projectUUID string
}

type planApplyEnvelope struct {
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result"`
	Context struct {
		ProjectUUID  string `json:"projectUuid"`
		ProjectName  string `json:"projectName"`
		DocumentType string `json:"documentType"`
	} `json:"context"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	} `json:"error"`
}

func (b *planApplyLive) call(action string, payload any) (*planApplyEnvelope, error) {
	body, err := postAction(b.cfg, action, b.window, payload, defaultActionTimeout)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	var env planApplyEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", action, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s: %s %s", action, env.Error.Code, env.Error.Message, env.Error.Detail)
		}
		return nil, fmt.Errorf("%s: %w", action, errActionFailed)
	}
	return &env, nil
}

func (b *planApplyLive) checkTarget(env *planApplyEnvelope, action string) error {
	if env.Context.ProjectUUID != b.projectUUID {
		return fmt.Errorf("%s answered for project %q (%s), not the expected --project-uuid %s; the window changed — stop, find the window again and re-run", action, env.Context.ProjectName, env.Context.ProjectUUID, b.projectUUID)
	}
	if env.Context.DocumentType != "pcb" {
		return fmt.Errorf("%s answered for a %q document, not a PCB; open the PCB (`easyeda doc open`) and re-run", action, env.Context.DocumentType)
	}
	return nil
}

func (b *planApplyLive) Read() (*planApplyState, error) {
	env, err := b.call("pcb.line.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	if err := b.checkTarget(env, "pcb.line.list"); err != nil {
		return nil, err
	}
	var lines struct {
		Lines []struct {
			PrimitiveID string  `json:"primitiveId"`
			Net         string  `json:"net"`
			Layer       int     `json:"layer"`
			StartX      float64 `json:"startX"`
			StartY      float64 `json:"startY"`
			EndX        float64 `json:"endX"`
			EndY        float64 `json:"endY"`
			LineWidth   float64 `json:"lineWidth"`
			Locked      bool    `json:"locked"`
		} `json:"lines"`
		Arcs []struct {
			Net string `json:"net"`
		} `json:"arcs"`
		ArcsAvailable *bool `json:"arcsAvailable"`
	}
	if err := json.Unmarshal(env.Result, &lines); err != nil {
		return nil, fmt.Errorf("pcb.line.list: decode result: %w", err)
	}
	if lines.ArcsAvailable != nil && !*lines.ArcsAvailable {
		return nil, fmt.Errorf("pcb.line.list could not read arc tracks; plan-apply will not guess that there are none")
	}
	st := &planApplyState{Arcs: map[string]int{}}
	for _, l := range lines.Lines {
		st.Lines = append(st.Lines, planApplyLiveLine{ID: l.PrimitiveID, Locked: l.Locked,
			planApplyTrack: planApplyTrack{Net: l.Net, Layer: l.Layer, X1: l.StartX, Y1: l.StartY, X2: l.EndX, Y2: l.EndY, Width: l.LineWidth}})
	}
	for _, a := range lines.Arcs {
		st.Arcs[a.Net]++
	}
	env, err = b.call("pcb.via.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	if err := b.checkTarget(env, "pcb.via.list"); err != nil {
		return nil, err
	}
	var vias struct {
		Vias []struct {
			PrimitiveID string  `json:"primitiveId"`
			Net         string  `json:"net"`
			X           float64 `json:"x"`
			Y           float64 `json:"y"`
			Hole        float64 `json:"holeDiameter"`
			Diameter    float64 `json:"diameter"`
			Locked      bool    `json:"locked"`
		} `json:"vias"`
	}
	if err := json.Unmarshal(env.Result, &vias); err != nil {
		return nil, fmt.Errorf("pcb.via.list: decode result: %w", err)
	}
	for _, v := range vias.Vias {
		st.Vias = append(st.Vias, planApplyLiveVia{ID: v.PrimitiveID, Locked: v.Locked, planApplyVia: planApplyVia{Net: v.Net, X: v.X, Y: v.Y, HoleMil: v.Hole, DiameterMil: v.Diameter}})
	}
	return st, nil
}

func (b *planApplyLive) DeleteLines(ids []string) error {
	_, err := b.call("pcb.route.delete", map[string]any{"primitiveIds": ids, "kind": "track"})
	return err
}

func (b *planApplyLive) DeleteVias(ids []string) error {
	_, err := b.call("pcb.route.delete", map[string]any{"primitiveIds": ids, "kind": "via"})
	return err
}

func (b *planApplyLive) AddLine(t planApplyTrack) error {
	_, err := b.call("pcb.line.create", map[string]any{"startX": t.X1, "startY": t.Y1, "endX": t.X2, "endY": t.Y2, "net": t.Net, "layer": t.Layer, "lineWidth": t.Width})
	return err
}

func (b *planApplyLive) AddVia(v planApplyVia) error {
	payload := map[string]any{"x": v.X, "y": v.Y, "net": v.Net}
	if v.HoleMil > 0 && v.DiameterMil > 0 {
		payload["holeDiameter"], payload["diameter"] = v.HoleMil, v.DiameterMil
	}
	_, err := b.call("pcb.via.create", payload)
	return err
}

// Lock sets the primitive lock on exactly these ids and returns how many the
// editor reports in the locked state. A short count or any failure is an error.
func (b *planApplyLive) Lock(ids []string) (int, error) {
	env, err := b.call("pcb.track.lock", map[string]any{"primitiveIds": ids, "locked": true})
	if err != nil {
		return 0, err
	}
	var res struct {
		Total    int      `json:"total"`
		Failures []string `json:"failures"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		return 0, fmt.Errorf("pcb.track.lock: decode result: %w", err)
	}
	if len(res.Failures) > 0 {
		return res.Total, fmt.Errorf("pcb.track.lock: %d primitive(s) failed to lock, first: %s", len(res.Failures), res.Failures[0])
	}
	if res.Total != len(ids) {
		return res.Total, fmt.Errorf("pcb.track.lock: locked %d of %d primitive(s)", res.Total, len(ids))
	}
	return res.Total, nil
}

func newPcbPlanApplyCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		planPath, projectUUID, layerMap string
		replace                         []string
		passes                          int
		dryRun                          bool
	)
	c := &cobra.Command{
		Use:   "plan-apply",
		Short: "Write an offline routing plan to the PCB and prove the copper matches it",
		Long: "Replace the copper of the plan's nets with the plan's tracks and vias, then read the\n" +
			"board back and compare it with the plan per net and layer: total track length,\n" +
			"width-weighted length, and the set of via positions. Net/layers that are off are\n" +
			"cleared and rewritten, at most --passes times. Tracks the editor merges into a new\n" +
			"primitive while others are deleted are swept up, not left behind.\n\n" +
			"Copper of every other net must come out identical, or the command fails.\n" +
			"A plan net marked failed ends up with no copper. Arc tracks and locked copper on a\n" +
			"net to replace are refused, not worked around.\n\n" +
			"The command does not save, rebuild pours or run DRC: do those explicitly afterwards.\n" +
			"Exit status is non-zero unless the board was proven to match the plan.\n\n" +
			"Plan file: a JSON array, one entry per net:\n" +
			`  {"net":"SDA","width":5,"lines":[{"Layer":0,"X1":..,"Y1":..,"X2":..,"Y2":..}],"vias":[[x,y]],"failed":false}` + "\n" +
			"Units are mil, y up. Layer is a plan index mapped to an EasyEDA layer id by --layer-map.",
		Example: "  easyeda pcb plan-apply --plan plan.json --project-uuid <uuid> --dry-run\n" +
			"  easyeda pcb plan-apply --plan plan.json --project-uuid <uuid>\n" +
			"  easyeda pcb plan-apply --plan plan.json --project-uuid <uuid> --replace-nets SDA,SCL,OLD_NET",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planPath == "" || strings.TrimSpace(projectUUID) == "" {
				return fmt.Errorf("--plan and --project-uuid are required")
			}
			if passes < 1 || passes > 5 {
				return fmt.Errorf("--passes must be 1..5")
			}
			lm, err := parsePlanApplyLayerMap(layerMap)
			if err != nil {
				return err
			}
			target, err := loadPlanApplyTarget(planPath, lm, replace)
			if err != nil {
				return err
			}
			live := &planApplyLive{cfg: cfg, window: *window, projectUUID: strings.TrimSpace(projectUUID)}
			rep, runErr := runPlanApply(live, target, passes, dryRun)
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return err
			}
			return runErr
		},
	}
	c.Flags().StringVar(&planPath, "plan", "", "routing plan JSON (required)")
	c.Flags().StringVar(&projectUUID, "project-uuid", "", "expected project UUID of the target window (required); a mismatch stops the run")
	c.Flags().StringSliceVar(&replace, "replace-nets", nil, "nets whose existing copper is replaced (default: the plan's nets); must include every plan net")
	c.Flags().StringVar(&layerMap, "layer-map", "0=1,1=2,2=16", "plan layer index → EasyEDA layer id")
	c.Flags().IntVar(&passes, "passes", 3, "maximum clear-and-rewrite passes (1..5)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "read and compare only; report what would be deleted and created")
	return c
}
