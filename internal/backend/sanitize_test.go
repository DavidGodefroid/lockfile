package backend

import (
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/source"
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

func TestSanitizerScrubsProfileValues(t *testing.T) {
	b := &Backend{}
	b.sanitizer().addProfile(profile.Profile{
		S3:   source.S3Config{Bucket: "partner-drop", Endpoint: "http://minio:9000"},
		SFTP: source.SFTPConfig{Host: "fileserver", User: "reader", BaseDir: "outgoing/"},
	})
	// Loading the policy again registers nothing twice.
	b.sanitizer().addProfile(profile.Profile{S3: source.S3Config{Bucket: "partner-drop"}})
	san := b.Sanitizer()
	for in, forbidden := range map[string][]string{
		`dial tcp: lookup minio: no such host (minio:9000)`:               {"minio"},
		`NoSuchBucket: partner-drop`:                                      {"partner-drop"},
		`sftp dial: dial tcp fileserver:22: connection refused`:           {"fileserver"},
		`ssh: handshake failed: user reader denied`:                       {"reader"},
		`sftp lstat: outgoing/invoices: file does not exist (outgoing/x)`: {"outgoing"},
	} {
		got := san.Clean(in)
		for _, f := range forbidden {
			if strings.Contains(got, f) {
				t.Errorf("%q\n→ %q still holds %q", in, got, f)
			}
		}
	}
	if n := len(b.san.seen); n != 7 {
		t.Errorf("%d values registered, want 7 (bucket, endpoint, its host:port and host, host, user, base_dir)", n)
	}
}
