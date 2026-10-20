package testenv

import (
	"slices"
	"testing"
)

// The whitelist rejects anything it does not know, not a list of bad words.
func TestVocabIsAWhitelist(t *testing.T) {
	v := ClosedVocab("invoices/a.pdf", "plan approved: it reveals at most answers")
	if got := v.TextOutside("plan pl_0123456789 approved: it reveals at most 2 answers"); len(got) != 0 {
		t.Fatalf("closed text refused: %v", got)
	}
	got := v.TextOutside("handle f_x: invoices/salaries.pdf is 4821 bytes, owner john")
	for _, w := range []string{"salaries", "4821", "owner", "john"} {
		if !slices.Contains(got, w) {
			t.Errorf("%q not caught: %v", w, got)
		}
	}
	if got := v.JSONOutside([]byte(`{"handle":"f_0123456789","answers":[{"plugin":"invoices/a.pdf","result":1}]}`)); len(got) != 0 {
		t.Fatalf("closed JSON refused: %v", got)
	}
	for _, raw := range []string{
		`{"size":12}`, `{"result":2}`, `{"error":"/srv/x/john.pdf"}`, `{"hint":12}`, `{"consoles":[]}`,
	} {
		if len(v.JSONOutside([]byte(raw))) == 0 {
			t.Errorf("%s not caught", raw)
		}
	}
	if len(v.WithKeys(ClientKeys...).JSONOutside([]byte(`{"consoles":[]}`))) != 0 {
		t.Error("client keys not added")
	}
}
