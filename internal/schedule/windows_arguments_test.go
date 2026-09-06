package schedule

import (
	"encoding/xml"
	"testing"
)

func TestWindowsTaskPathArguments(t *testing.T) {
	for _, tc := range []struct{ path, quoted string }{
		{`C:\data`, `"C:\data"`},
		{`C:\数据 space`, `"C:\数据 space"`},
		{`C:\`, `"C:\\"`},
		{`C:\data space\`, `"C:\data space\\"`},
		{`\\server\share\`, `"\\server\share\\"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			cfg := Config{Executable: `C:\Program Files\weibo-exp.exe`, DataDir: tc.path, Hour: 10}
			data, err := RenderWindowsTaskXML(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var task struct {
				Arguments string `xml:"Actions>Exec>Arguments"`
			}
			if err := xml.Unmarshal(data, &task); err != nil {
				t.Fatal(err)
			}
			if want := "--data-dir " + tc.quoted + " scheduled-run"; task.Arguments != want {
				t.Fatalf("arguments = %q, want %q", task.Arguments, want)
			}
			exe, dir := windowsArguments(string(data))
			if exe != cfg.Executable || dir != cfg.DataDir {
				t.Fatalf("inspection = %q %q, want %q %q", exe, dir, cfg.Executable, cfg.DataDir)
			}
		})
	}
}

func TestWindowsTaskRejectsQuoteInPath(t *testing.T) {
	for _, cfg := range []Config{
		{Executable: `C:\bad".exe`, DataDir: `C:\data`, Hour: 10},
		{Executable: `C:\weibo-exp.exe`, DataDir: `C:\bad"`, Hour: 10},
	} {
		if _, err := RenderWindowsTaskXML(cfg); err == nil {
			t.Fatal("accepted a quote, which is not valid in a Windows file path")
		}
	}
}
