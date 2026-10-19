package product

import (
	"encoding/json"
	"testing"

	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
)

func TestProductValid(t *testing.T) {
	if err := P.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEveryCodeMapped(t *testing.T) {
	want := map[answers.Code]refusal.Retry{
		answers.CodeKeyOutsidePrefix:   refusal.RetryRewrite,
		answers.CodeUnknownProfile:     refusal.RetryRewrite,
		answers.CodeFetchFailed:        refusal.RetryStop,
		answers.CodeTooManyPlugins:     refusal.RetryRewrite,
		answers.CodePluginNotInstalled: refusal.RetryAskUser,
		answers.CodePluginTampered:     refusal.RetryStop,
		answers.CodePluginYanked:       refusal.RetryAskUser,
		answers.CodeSandboxUnavailable: refusal.RetryStop,
		answers.CodeHandleExpired:      refusal.RetryRewrite,
	}
	for _, c := range answers.Codes {
		switch c {
		case answers.CodePluginError, answers.CodeApprovalDenied, answers.CodeApprovalTimeout:
			if Mapped(c) {
				t.Errorf("%s must not be a refusal", c)
			}
			continue
		}
		r, ok := want[c]
		if !ok {
			t.Fatalf("code %s has no expectation", c)
		}
		rc := Code(c)
		if !rc.Known() {
			t.Errorf("%s → %s is not registered", c, rc)
		}
		if rc.Retry() != r {
			t.Errorf("%s retry = %s, want %s", c, rc.Retry(), r)
		}
	}
}

func TestRefuseCarriesOnlyCode(t *testing.T) {
	resp := Refuse(7, answers.CodeFetchFailed)
	if resp.Error == nil || resp.Error.Code != ipc.CodeRefused || resp.Error.Message != "fetch_failed" {
		t.Fatalf("resp = %+v", resp.Error)
	}
	b, err := json.Marshal(resp.Error.Data)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		switch k {
		case "code", "retry", "hint":
		default:
			t.Errorf("unexpected data key %q", k)
		}
	}
	if m["code"] != "E_FETCH_FAILED" {
		t.Errorf("code = %v", m["code"])
	}
}

func TestApprovalMessagesFixed(t *testing.T) {
	denied := ipc.Response{ID: 3, Error: &ipc.RPCError{Code: ipc.CodeDenied, Message: "denied by the human: it is 0 bytes; do not retry unless asked"}}
	got := ApprovalRefusal(3, denied)
	if got.Error.Code != ipc.CodeDenied || got.Error.Message != "approval_denied" || got.Error.Data != nil {
		t.Fatalf("denied → %+v", got.Error)
	}
	timeout := ipc.Response{ID: 3, Error: &ipc.RPCError{Code: ipc.CodeTimeout, Message: "no answer"}}
	if got := ApprovalRefusal(3, timeout); got.Error.Code != ipc.CodeTimeout || got.Error.Message != "approval_timeout" {
		t.Fatalf("timeout → %+v", got.Error)
	}
	other := ipc.Response{ID: 3, Error: &ipc.RPCError{Code: ipc.CodeInternal, Message: "/srv/x"}}
	if got := ApprovalRefusal(3, other); got.Error.Code != ipc.CodeDenied || got.Error.Message != "approval_denied" {
		t.Fatalf("other → %+v", got.Error)
	}
}
