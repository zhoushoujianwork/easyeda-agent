package app

// cmd_pcb_autoroute_official.go — `pcb autoroute` on the official router.
//
// Preferred path (EDA >= 4.1.79): the native job model. pcb.route.auto.start
// returns a jobId at once; this side polls pcb.route.auto.status, prints the
// host's own progress, and on --timeout or Ctrl-C calls pcb.route.auto.cancel
// and waits a bounded grace for the host to confirm a terminal state. Every
// call is short, so the connector queue is never held by the router (#308).
//
// Fallback (older host or connector): the single blocking pcb.route.auto call
// (eda.pcb_Document.autoRouting). It cannot report progress or be cancelled;
// the CLI says so instead of pretending its timeout stopped the router.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
)

// buildOfficialAutoroutePayload validates the flags into the pcb.route.auto payload.
func buildOfficialAutoroutePayload(nets, ignoreNets []string, layers []int, corner, optimize string) (map[string]any, error) {
	if corner != "45" && corner != "90" {
		return nil, fmt.Errorf("--corner must be 45 or 90 (got %q)", corner)
	}
	if optimize != "completion" && optimize != "faster" {
		return nil, fmt.Errorf("--optimize must be completion or faster (got %q)", optimize)
	}
	payload := map[string]any{"cornerStyle": corner, "optimization": optimize}
	if len(nets) > 0 {
		payload["nets"] = nets
	}
	if len(ignoreNets) > 0 {
		payload["ignoreNets"] = ignoreNets
	}
	if len(layers) > 0 {
		payload["layers"] = layers
	}
	return payload, nil
}

// autorouteJobCall runs one short job-model action and returns its result.
type autorouteJobCall func(action string, payload map[string]any) (map[string]any, error)

// autorouteFollow carries the job-polling knobs; tests shrink the intervals
// and replace the clock, the calls and the interrupt source.
type autorouteFollow struct {
	call        autorouteJobCall
	timeout     time.Duration // whole-run budget; 0 = no deadline
	interval    time.Duration // progress poll / event interval
	cancelGrace time.Duration // how long the host may take to confirm a cancel
	now         func() time.Time
	sleep       func(time.Duration, <-chan os.Signal) bool // false = interrupted
	interrupt   <-chan os.Signal
}

const (
	autorouteJobPollInterval = 5 * time.Second
	autorouteJobCancelGrace  = 10 * time.Second
	autorouteJobCallTimeout  = 30 * time.Second
)

func runOfficialAutoroute(cfg *appConfig, window string, nets, ignoreNets []string, layers []int, corner, optimize string,
	timeout time.Duration, jobID string, stdout, stderr io.Writer) error {
	call := func(action string, payload map[string]any) (map[string]any, error) {
		res, err := requestActionTimed(cfg, action, window, payload, autorouteJobCallTimeout)
		if res == nil {
			return nil, err
		}
		return res.Result, err
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	f := autorouteFollow{
		call: call, timeout: timeout, interval: autorouteJobPollInterval, cancelGrace: autorouteJobCancelGrace,
		now: time.Now, sleep: sleepOrInterrupt, interrupt: interrupt,
	}
	if jobID != "" {
		fmt.Fprintf(stderr, "following existing auto-route job %s\n", jobID)
		return f.follow(jobID, stdout, stderr)
	}
	payload, err := buildOfficialAutoroutePayload(nets, ignoreNets, layers, corner, optimize)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "official auto-route job (existing copper kept): %v\n", payload)
	started, err := call("pcb.route.auto.start", payload)
	if err != nil {
		if autorouteJobUnsupported(err) {
			fmt.Fprintf(stderr, "auto-route job model unavailable (%v)\n", err)
			return runBlockingAutoroute(cfg, window, payload, timeout, stdout, stderr)
		}
		return err
	}
	id, _ := started["jobId"].(string)
	if id == "" {
		return fmt.Errorf("pcb.route.auto.start returned no jobId: %v", started)
	}
	fmt.Fprintf(stderr, "job %s started; progress every %s; --timeout %s cancels it (Ctrl-C too)\n", id, f.interval, timeout)
	return f.follow(id, stdout, stderr)
}

// autorouteJobUnsupported: the connector (UNKNOWN_ACTION) or the host
// (PRECONDITION_REFUSED "unsupported") has no job model. Nothing was started.
func autorouteJobUnsupported(err error) bool {
	var ae *actionError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == "UNKNOWN_ACTION" || (ae.Code == "PRECONDITION_REFUSED" && strings.Contains(ae.Message, "unsupported"))
}

func sleepOrInterrupt(d time.Duration, interrupt <-chan os.Signal) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-interrupt:
		return false
	}
}

// follow polls the job until it is terminal, or cancels it on deadline/interrupt.
func (f autorouteFollow) follow(jobID string, stdout, stderr io.Writer) error {
	begin := f.now()
	failures := 0
	for {
		st, err := f.call("pcb.route.auto.status", map[string]any{"jobId": jobID})
		if err != nil {
			failures++
			fmt.Fprintf(stderr, "status poll failed (%d/3): %v\n", failures, err)
			if failures >= 3 {
				return f.cancel(jobID, "status unreadable", begin, stdout, stderr)
			}
		} else {
			failures = 0
			if b, _ := st["terminal"].(bool); b {
				return finishAutorouteJob(st, "", stdout, stderr)
			}
			fmt.Fprintln(stderr, autorouteProgressLine(st, f.now().Sub(begin)))
		}
		if f.timeout > 0 && f.now().Sub(begin) >= f.timeout {
			return f.cancel(jobID, fmt.Sprintf("--timeout %s reached", f.timeout), begin, stdout, stderr)
		}
		if !f.sleep(f.interval, f.interrupt) {
			return f.cancel(jobID, "interrupted", begin, stdout, stderr)
		}
	}
}

// cancel asks the host to stop the job and waits cancelGrace for a terminal
// state. Only a host-confirmed terminal state counts as stopped.
func (f autorouteFollow) cancel(jobID, why string, begin time.Time, stdout, stderr io.Writer) error {
	fmt.Fprintf(stderr, "%s: cancelling job %s\n", why, jobID)
	res, err := f.call("pcb.route.auto.cancel", map[string]any{"jobId": jobID})
	if err != nil {
		return fmt.Errorf("%s; cancel of job %s failed: %w — the router may still be running; check with `easyeda pcb autoroute --job %s`", why, jobID, err, jobID)
	}
	if ok, _ := res["cancelRequested"].(bool); !ok {
		fmt.Fprintf(stderr, "host did not accept the cancel request for job %s; checking its state\n", jobID)
	}
	cancelAt := f.now()
	for {
		st, err := f.call("pcb.route.auto.status", map[string]any{"jobId": jobID})
		if err == nil {
			if b, _ := st["terminal"].(bool); b {
				fmt.Fprintf(stderr, "host confirmed job %s %v %.1fs after the cancel request\n", jobID, st["state"], f.now().Sub(cancelAt).Seconds())
				return finishAutorouteJob(st, why, stdout, stderr)
			}
		}
		if f.now().Sub(cancelAt) >= f.cancelGrace {
			state := "unreadable"
			if st != nil {
				state = fmt.Sprint(st["state"])
			}
			return fmt.Errorf("%s; host did not confirm cancellation of job %s within %s (state=%s, %s since start). "+
				"The connector queue is not held by this job, but the router may still commit copper later; "+
				"re-check with `easyeda pcb autoroute --job %s`, then pcb save → doc reload → read back",
				why, jobID, f.cancelGrace, state, f.now().Sub(begin).Round(time.Second), jobID)
		}
		f.sleep(time.Second, nil)
	}
}

// autorouteProgressLine prints only what the host reported: no derived percent.
func autorouteProgressLine(st map[string]any, elapsed time.Duration) string {
	parts := []string{fmt.Sprintf("autoroute job %v: %v, elapsed %s", st["jobId"], st["state"], elapsed.Round(time.Second))}
	if p, ok := st["hostProgress"].(float64); ok {
		parts = append(parts, fmt.Sprintf("host progress %v", p))
	}
	if eng, ok := st["engine"].(map[string]any); ok {
		if r, ok := eng["routabitity"].(float64); ok {
			parts = append(parts, fmt.Sprintf("engine routability %.3f", r))
		}
	}
	if l, ok := st["lastLog"].(map[string]any); ok && l["message"] != nil {
		parts = append(parts, fmt.Sprintf("last log: %v", l["message"]))
	}
	return strings.Join(parts, ", ")
}

// finishAutorouteJob prints the terminal job report and turns it into an exit
// status: completed with new copper and no locked loss is the only success.
func finishAutorouteJob(st map[string]any, stopReason string, stdout, stderr io.Writer) error {
	if err := writeJSON(stdout, st); err != nil {
		return err
	}
	state := fmt.Sprint(st["state"])
	tracked, _ := st["tracked"].(bool)
	created, _ := st["created"].(map[string]any)
	if tracked {
		fmt.Fprintf(stderr, "job %v %s: created %v; removed: %v; partial=%v\n", st["jobId"], state, created, st["removedPrimitiveIds"], st["partial"])
	} else {
		fmt.Fprintf(stderr, "job %v %s: copper delta unknown (this connector did not start the job) — read back before trusting it\n", st["jobId"], state)
	}
	if e := st["error"]; e != nil {
		fmt.Fprintf(stderr, "host error: %v\n", e)
	}
	if logs, _ := st["errorLogs"].([]any); len(logs) > 0 {
		for _, l := range logs {
			fmt.Fprintf(stderr, "router error log: %v\n", l)
		}
	}
	if lost, _ := st["lockedLost"].([]any); len(lost) > 0 {
		return fmt.Errorf("the router removed %d locked primitive(s) %v — restore them (no undo) before continuing", len(lost), lost)
	}
	if err := autorouteScopeError(st); err != nil {
		return err
	}
	fmt.Fprintln(stderr, "next: pcb save → doc reload → pcb drc / pcb check (routing results read before a reload may be stale)")
	newCopper := 0.0
	for _, k := range []string{"tracks", "arcs", "vias"} {
		if v, ok := created[k].(float64); ok {
			newCopper += v
		}
	}
	switch {
	case stopReason != "":
		return fmt.Errorf("auto-route stopped (%s): job %v %s, %v new copper primitive(s)", stopReason, st["jobId"], state, newCopper)
	case state != "completed":
		return fmt.Errorf("auto-route job %v ended %s (error: %v)", st["jobId"], state, st["error"])
	case tracked && newCopper == 0 && len(asSlice(st["removedPrimitiveIds"])) == 0:
		return fmt.Errorf("auto-route job %v completed but added no copper", st["jobId"])
	}
	return nil
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

// runBlockingAutoroute is the legacy single-call path for hosts without the job
// model. Its timeout only ends the CLI wait: the host router keeps running and
// keeps the connector queue until it finishes on its own.
func runBlockingAutoroute(cfg *appConfig, window string, payload map[string]any, timeout time.Duration, stdout, stderr io.Writer) error {
	fmt.Fprintf(stderr, "falling back to blocking autoRouting: no progress, and it cannot be cancelled — if --timeout %s expires the router keeps running and holds the connector queue until it finishes (host cap 15 min); upgrade EasyEDA (>= 4.1.79) and the connector for the job model\n", timeout)
	res, err := requestActionTimed(cfg, "pcb.route.auto", window, payload, timeout)
	if err != nil {
		return err
	}
	if err := writeJSON(stdout, res.Result); err != nil {
		return err
	}
	if router, ok := res.Result["router"].(map[string]any); ok {
		fmt.Fprintf(stderr, "router: success=%v nets %v/%v routed, failed=%v, %vms\n",
			router["success"], router["successNetsCount"], router["totalNetsCount"], router["failedNets"], router["duration"])
	}
	fmt.Fprintf(stderr, "created: %v; removed: %v\n", res.Result["created"], res.Result["removedPrimitiveIds"])
	if lost, _ := res.Result["lockedLost"].([]any); len(lost) > 0 {
		return fmt.Errorf("the router removed %d locked primitive(s) %v — restore them (no undo) before continuing", len(lost), lost)
	}
	if err := autorouteScopeError(res.Result); err != nil {
		return err
	}
	fmt.Fprintln(stderr, "next: pcb save → doc reload → pcb drc / pcb check (routing results read before a reload may be stale)")
	return nil
}

// autorouteScopeError fails a --net run that created copper on other nets. The
// connector scopes the router through ignoreNets (the host ignores RoutingNets'
// name list) and reports outOfScopeNets from its before/after inventory, so a
// host that still strays is caught instead of silently routing the whole board.
func autorouteScopeError(result map[string]any) error {
	out := asSlice(result["outOfScopeNets"])
	if len(out) == 0 {
		return nil
	}
	return fmt.Errorf("auto-route created copper outside the requested --net scope on %d net(s) %v (createdByNet %v) — inspect, then `pcb rip-up --net` those nets before continuing", len(out), out, result["createdByNet"])
}
