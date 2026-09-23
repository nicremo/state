package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const plutilPath = "/usr/bin/plutil"

func testSpec(t *testing.T) ServiceSpec {
	t.Helper()
	root := t.TempDir()
	return ServiceSpec{
		Executable: filepath.Join(root, "bin", "state-runner"),
		ConfigPath: filepath.Join(root, "config", "runner.json"),
		LogDir:     filepath.Join(root, "Library", "Logs", "State Runner"),
		Path:       "/usr/local/bin:/usr/bin:/bin",
		Home:       filepath.Join(root, "home"),
	}
}

func TestLaunchAgentPlistDescribesTheRunnerAgent(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	contents, err := LaunchAgentPlist(spec)
	if err != nil {
		t.Fatalf("LaunchAgentPlist() error = %v", err)
	}
	plist := string(contents)

	required := []string{
		"<string>" + ServiceLabel + "</string>",
		"<string>" + spec.Executable + "</string>",
		"<string>run</string>",
		"<string>--config</string>",
		"<string>" + spec.ConfigPath + "</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<string>Background</string>",
		"<integer>30</integer>",
		"<string>" + filepath.Join(spec.LogDir, "runner.out.log") + "</string>",
		"<string>" + filepath.Join(spec.LogDir, "runner.err.log") + "</string>",
		"<key>PATH</key>",
		"<string>" + spec.Path + "</string>",
		"<key>HOME</key>",
		"<string>" + spec.Home + "</string>",
	}
	for _, want := range required {
		if !strings.Contains(plist, want) {
			t.Errorf("plist misses %q\n%s", want, plist)
		}
	}

	arguments := strings.Count(plist, "<string>"+spec.Executable+"</string>") +
		strings.Count(plist, "<string>run</string>") +
		strings.Count(plist, "<string>--config</string>") +
		strings.Count(plist, "<string>"+spec.ConfigPath+"</string>")
	if arguments != 4 {
		t.Errorf("ProgramArguments entries = %d, want 4", arguments)
	}
}

func TestLaunchAgentPlistPassesPlutilLint(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(plutilPath); err != nil {
		t.Skipf("%s is not available", plutilPath)
	}
	contents, err := LaunchAgentPlist(testSpec(t))
	if err != nil {
		t.Fatalf("LaunchAgentPlist() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), ServiceLabel+".plist")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	if output, err := exec.Command(plutilPath, "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint failed: %v\n%s\n%s", err, output, contents)
	}
}

func TestLaunchAgentPlistEscapesValues(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	spec.Executable = filepath.Join(spec.Executable, "at&t")
	spec.ConfigPath = filepath.Join(spec.ConfigPath, "a<b>c")
	contents, err := LaunchAgentPlist(spec)
	if err != nil {
		t.Fatalf("LaunchAgentPlist() error = %v", err)
	}
	plist := string(contents)
	if !strings.Contains(plist, "at&amp;t") || !strings.Contains(plist, "a&lt;b&gt;c") {
		t.Fatalf("plist does not escape XML characters\n%s", plist)
	}
	if strings.Contains(plist, "at&t") || strings.Contains(plist, "a<b>c") {
		t.Fatalf("plist contains unescaped XML characters\n%s", plist)
	}
}

func TestLaunchAgentPlistRejectsInvalidSpecs(t *testing.T) {
	t.Parallel()

	absolute := testSpec(t)
	cases := []struct {
		name   string
		mutate func(*ServiceSpec)
	}{
		{name: "relative executable", mutate: func(spec *ServiceSpec) { spec.Executable = "state-runner" }},
		{name: "empty executable", mutate: func(spec *ServiceSpec) { spec.Executable = "" }},
		{name: "relative config", mutate: func(spec *ServiceSpec) { spec.ConfigPath = "runner.json" }},
		{name: "empty config", mutate: func(spec *ServiceSpec) { spec.ConfigPath = "" }},
		{name: "relative log dir", mutate: func(spec *ServiceSpec) { spec.LogDir = "logs" }},
		{name: "empty log dir", mutate: func(spec *ServiceSpec) { spec.LogDir = "" }},
		{name: "relative home", mutate: func(spec *ServiceSpec) { spec.Home = "home" }},
		{name: "empty home", mutate: func(spec *ServiceSpec) { spec.Home = "" }},
		{name: "empty path", mutate: func(spec *ServiceSpec) { spec.Path = "" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			spec := absolute
			testCase.mutate(&spec)
			if _, err := LaunchAgentPlist(spec); err == nil {
				t.Fatalf("LaunchAgentPlist(%+v) succeeded, want error", spec)
			}
		})
	}
}
