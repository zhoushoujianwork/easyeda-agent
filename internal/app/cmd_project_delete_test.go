package app

import (
	"bytes"
	"testing"
)

func TestProjectDeletePreviewAndConfirmation(t *testing.T) {
	cfg, captured, cleanup := newCapturingDaemon(t)
	defer cleanup()
	var out, errOut bytes.Buffer
	c := newProjectCmd(cfg, &out, &errOut)
	c.SetArgs([]string{"delete", "--window", "w1", "--project-uuid", "1234567890abcdef", "--expected-name", "disposable", "--dry-run"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.action != "project.delete_preview" || captured.payload["expectedFriendlyName"] != "disposable" {
		t.Fatal(captured)
	}
	c = newProjectCmd(cfg, &out, &errOut)
	c.SetArgs([]string{"delete", "--window", "w1", "--project-uuid", "1234567890abcdef", "--expected-name", "disposable"})
	if err := c.Execute(); err == nil {
		t.Fatal("unconfirmed deletion accepted")
	}
	if captured.action != "project.delete_preview" {
		t.Fatal("unconfirmed request reached daemon")
	}
}

func TestProjectDeletionRequiresBooleanVerification(t *testing.T) {
	for _, input := range []string{`{}`, `{"result":{"deleted":true}}`, `{"result":{"deleted":"true","verified":true,"partial":false}}`, `{"result":{"deleted":true,"verified":true,"partial":true}}`} {
		if projectDeletionVerified([]byte(input)) {
			t.Fatalf("accepted %s", input)
		}
	}
	if !projectDeletionVerified([]byte(`{"result":{"deleted":true,"verified":true,"partial":false}}`)) {
		t.Fatal("complete verification rejected")
	}
}
