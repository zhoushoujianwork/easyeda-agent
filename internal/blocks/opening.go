package blocks

import "encoding/json"

// ConnectorOpening declares, for a footprint, which way its opening (the wire-entry
// / plug face) points in the footprint's LOCAL (rotation-0) frame. This is a
// physical fact that is NOT in the PCB pad/copper geometry (a symmetric 2-pin screw
// terminal looks the same either way), so it must be block-declared — the placer
// consumes it to rotate the connector so its opening faces OFF-board. Block-level
// `openings: [{match, local}]`, keyed by device-name substring so a PLACED part
// resolves by its manufacturerId (e.g. "KF301-5.0-2P" → match "kf301").
type ConnectorOpening struct {
	Match  string `json:"match"` // device-name substring this applies to (e.g. "kf301")
	Local  string `json:"local"` // opening dir in the footprint's local frame: +x | -x | +y | -y
	Reason string `json:"reason"`
}

// LoadConnectorOpenings aggregates every block's declared connector openings. A
// block without any, or a malformed one, is skipped (best-effort, never fatal).
func LoadConnectorOpenings() ([]ConnectorOpening, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	var out []ConnectorOpening
	for _, b := range all {
		var raw struct {
			Openings []ConnectorOpening `json:"openings"`
		}
		if json.Unmarshal(b.Raw, &raw) != nil {
			continue
		}
		for _, o := range raw.Openings {
			if o.Match != "" && o.Local != "" {
				out = append(out, o)
			}
		}
	}
	return out, nil
}
