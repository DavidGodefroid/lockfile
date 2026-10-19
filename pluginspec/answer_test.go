package pluginspec

import "testing"

func TestParseAnswerAccepts(t *testing.T) {
	cases := map[string]bool{"1": true, "0": false, "1\n": true, "0\n": false}
	for out, want := range cases {
		o := ParseAnswer([]byte(out), false, 0, false)
		if o.Answer == nil || *o.Answer != want || o.Cause != CauseNone {
			t.Errorf("%q: got %+v", out, o)
		}
	}
}

func TestParseAnswerRejects(t *testing.T) {
	type c struct {
		out       string
		truncated bool
		exit      int
		signaled  bool
		want      FailureCause
	}
	cases := []c{
		{" 1", false, 0, false, CauseInvalidOutput},
		{"01", false, 0, false, CauseInvalidOutput},
		{"1\n\n", false, 0, false, CauseInvalidOutput},
		{"1\r\n", false, 0, false, CauseInvalidOutput},
		{"true", false, 0, false, CauseInvalidOutput},
		{"", false, 0, false, CauseInvalidOutput},
		{"2", false, 0, false, CauseInvalidOutput},
		{"1\n", true, 0, false, CauseOutputTooLong},
		{"1", false, 1, false, CauseExitCode},
		{"0", false, 2, false, CauseExitCode},
		{"1", false, -1, true, CauseSignal},
	}
	for _, tc := range cases {
		o := ParseAnswer([]byte(tc.out), tc.truncated, tc.exit, tc.signaled)
		if o.Answer != nil || o.Cause != tc.want {
			t.Errorf("%+v: got answer=%v cause=%q", tc, o.Answer, o.Cause)
		}
	}
}
