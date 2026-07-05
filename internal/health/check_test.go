package health

import "testing"

func TestModeReParsesAdvertisedModes(t *testing.T) {
	tests := []struct {
		name         string
		ffmpegStderr string
		wantSize     string
		wantRate     string
		wantMatch    bool
	}{
		{
			name: "1080p60 with a live source",
			ffmpegStderr: `[in#0 @ 0x123] Selected video size (1x1) is not supported by the device.
[in#0 @ 0x123] Supported modes:
[in#0 @ 0x123]   1920x1080@[59.940180 59.940180]fps
[in#0 @ 0x123] Error opening input: Input/output error`,
			wantSize:  "1920x1080",
			wantRate:  "59.940180",
			wantMatch: true,
		},
		{
			name: "4K30 no-signal / default mode",
			ffmpegStderr: `[in#0 @ 0x123] Supported modes:
[in#0 @ 0x123]   3840x2160@[30.000030 30.000030]fps
[in#0 @ 0x123] Error opening input: Input/output error`,
			wantSize:  "3840x2160",
			wantRate:  "30.000030",
			wantMatch: true,
		},
		{
			name: "picks the first mode when several are listed",
			ffmpegStderr: `[in#0 @ 0x123] Supported modes:
[in#0 @ 0x123]   1920x1080@[59.940180 59.940180]fps
[in#0 @ 0x123]   3840x2160@[30.000030 30.000030]fps`,
			wantSize:  "1920x1080",
			wantRate:  "59.940180",
			wantMatch: true,
		},
		{
			name:         "no modes reported (wedged) yields no match",
			ffmpegStderr: `[in#0 @ 0x123] Error opening input: Input/output error`,
			wantMatch:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := modeRe.FindStringSubmatch(tt.ffmpegStderr)
			if tt.wantMatch != (m != nil) {
				t.Fatalf("match = %v, want %v", m != nil, tt.wantMatch)
			}
			if !tt.wantMatch {
				return
			}
			if m[1] != tt.wantSize {
				t.Errorf("size = %q, want %q", m[1], tt.wantSize)
			}
			if m[2] != tt.wantRate {
				t.Errorf("framerate = %q, want %q", m[2], tt.wantRate)
			}
		})
	}
}

func TestLastLines(t *testing.T) {
	in := "line1\n\nline2\nline3\n\nline4\n"
	got := lastLines(in, 2)
	want := "line3\nline4"
	if got != want {
		t.Errorf("lastLines = %q, want %q", got, want)
	}
	if got := lastLines("only one line", 5); got != "only one line" {
		t.Errorf("lastLines short = %q", got)
	}
}
