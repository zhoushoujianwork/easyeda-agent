package app

import "testing"

func win(id, proj string) healthWindow {
	var w healthWindow
	w.WindowID = id
	w.Context.ProjectName = proj
	return w
}

func TestSelectWindow(t *testing.T) {
	a := win("w-esp", "立创·实战派ESP32-S3开发板")
	b := win("w-moto", "motobox2026")

	// explicit --window always wins, even with many windows
	if id, err := selectWindow([]healthWindow{a, b}, "", "w-x"); err != nil || id != "w-x" {
		t.Fatalf("explicit window: %q %v", id, err)
	}
	// --project unique match (CJK name)
	if id, err := selectWindow([]healthWindow{a, b}, "立创·实战派ESP32-S3开发板", ""); err != nil || id != "w-esp" {
		t.Fatalf("project match: %q %v", id, err)
	}
	// sole window, no project
	if id, err := selectWindow([]healthWindow{b}, "", ""); err != nil || id != "w-moto" {
		t.Fatalf("sole window: %q %v", id, err)
	}
	// 2+ windows, no project → error
	if _, err := selectWindow([]healthWindow{a, b}, "", ""); err == nil {
		t.Fatal("expected multi-window error")
	}
	// project not found → error
	if _, err := selectWindow([]healthWindow{a, b}, "ghost", ""); err == nil {
		t.Fatal("expected no-match error")
	}
	// project maps to 2 windows → error
	if _, err := selectWindow([]healthWindow{win("w1", "dup"), win("w2", "dup")}, "dup", ""); err == nil {
		t.Fatal("expected ambiguous-project error")
	}
	// no windows → error
	if _, err := selectWindow(nil, "", ""); err == nil {
		t.Fatal("expected no-connector error")
	}
}
