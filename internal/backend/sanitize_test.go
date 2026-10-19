package backend

import (
	"strings"
	"testing"
)

func TestSanitizerScrubs(t *testing.T) {
	san := Sanitizer()
	for _, tc := range []struct {
		in        string
		forbidden []string
	}{
		{`operation error S3: HeadObject, https response error StatusCode: 403, RequestID: 4KQ9X2, HostID: abc+/=, api error AccessDenied: GET https://documents.s3.eu-west-1.amazonaws.com/invoices/2026/john-doe.pdf?X-Amz-Credential=AKIAABCDEFGHIJKLMNOP`,
			[]string{"john", "documents.s3", "AKIA", "4KQ9X2", "invoices/2026"}},
		{`sftp open: open /outgoing/john.pdf: permission denied`, []string{"/outgoing", "john"}},
		{`sftp dial: dial tcp sftp.example.org:22: connect: connection refused`, []string{"sftp.example.org", ":22"}},
		{`dial tcp 10.1.2.3:2222: i/o timeout`, []string{"10.1.2.3"}},
		{`Authorization: AWS4-HMAC-SHA256 Credential=AKIAABCDEFGHIJKLMNOP/x`, []string{"AWS4", "AKIA"}},
		{`s3 head object: open "/srv/samples/a b.pdf": no such file`, []string{"/srv/samples"}},
	} {
		got := san.Clean(tc.in)
		for _, f := range tc.forbidden {
			if strings.Contains(got, f) {
				t.Errorf("%q\n→ %q still holds %q", tc.in, got, f)
			}
		}
	}
	if got := san.Clean("plain error with nothing to hide"); got != "plain error with nothing to hide" {
		t.Errorf("plain text changed: %q", got)
	}
}
