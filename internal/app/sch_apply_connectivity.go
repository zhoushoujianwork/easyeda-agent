package app

import (
	"fmt"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
	"reflect"
)

func compareObserved(expected connectivity.Document, result map[string]any) error {
	live, err := connectivity.FromRead(result)
	if err != nil {
		return err
	}
	a, err := expected.NamedPins()
	if err != nil {
		return err
	}
	b, err := live.NamedPins()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("connectivity mismatch: live pin-to-net differs from expected snapshot; stop and re-plan")
	}
	return nil
}
func (r *applyRunner) checkConnectivity(d *connectivity.Document) (any, error) {
	cfg := *r.cfg
	cfg.doc = d.DocumentID
	cfg.project = d.ProjectID
	res, err := requestAction(staleReadOptIn(&cfg, "sch apply connectivity checkpoint"), "schematic.read", r.window, map[string]any{"includeCheck": false})
	if err != nil {
		return nil, err
	}
	if res.Context == nil || res.Context.DocumentUUID != d.DocumentID || res.Context.ProjectUUID != d.ProjectID {
		return nil, fmt.Errorf("connectivity checkpoint target mismatch")
	}
	if err = compareObserved(*d, res.Result); err != nil {
		return nil, err
	}
	return map[string]any{"matched": true}, nil
}
