package main

import "testing"

func TestParseArgs(t *testing.T) {
	tests := []struct {
		desc    string
		args    []string
		want    cliArgs
		wantErr bool
	}{
		{
			desc: "flags before dir",
			args: []string{"-o", "scan.json", "./books"},
			want: cliArgs{dir: "./books", out: "scan.json", ffprobe: "ffprobe"},
		},
		{
			desc: "flags after dir (trailing)",
			args: []string{"./books", "-o", "scan.json"},
			want: cliArgs{dir: "./books", out: "scan.json", ffprobe: "ffprobe"},
		},
		{
			desc: "flag value must not be mistaken for the dir",
			args: []string{"-o", "scan.json", "-ffprobe", "/usr/bin/ffprobe", "./books"},
			want: cliArgs{dir: "./books", out: "scan.json", ffprobe: "/usr/bin/ffprobe"},
		},
		{
			desc: "ffprobe disabled",
			args: []string{"-ffprobe", "", "./books"},
			want: cliArgs{dir: "./books", ffprobe: ""},
		},
		{
			desc: "bare dir, defaults",
			args: []string{"./books"},
			want: cliArgs{dir: "./books", ffprobe: "ffprobe"},
		},
		{desc: "no dir", args: nil, wantErr: true},
		{desc: "only flags, no dir", args: []string{"-o", "scan.json"}, wantErr: true},
		{desc: "two positionals", args: []string{"./a", "./b"}, wantErr: true},
		{desc: "unknown flag", args: []string{"-nope", "./books"}, wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%s: parseArgs(%v) = %+v, want error", tt.desc, tt.args, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: parseArgs(%v) error: %v", tt.desc, tt.args, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: parseArgs(%v) = %+v, want %+v", tt.desc, tt.args, got, tt.want)
		}
	}
}
