package runner

import (
	"errors"
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

type fakeLaunchctl struct {
	calls        []string
	printOutput  string
	printErr     error
	bootstrapErr error
	bootoutErr   error
}

func (fake *fakeLaunchctl) Bootstrap(domain string, plistPath string) error {
	fake.calls = append(fake.calls, "bootstrap "+domain+" "+plistPath)
	return fake.bootstrapErr
}

func (fake *fakeLaunchctl) Bootout(domain string, label string) error {
	fake.calls = append(fake.calls, "bootout "+domain+" "+label)
	return fake.bootoutErr
}

func (fake *fakeLaunchctl) Print(domain string, label string) (string, error) {
	fake.calls = append(fake.calls, "print "+domain+" "+label)
	return fake.printOutput, fake.printErr
}

func TestServiceManagerInstallWritesTheAgentAndBootstrapsIt(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	launchAgentsDir := filepath.Join(t.TempDir(), "LaunchAgents")
	launchctl := &fakeLaunchctl{}
	manager := NewServiceManager(launchAgentsDir, launchctl, 501)

	plistPath, err := manager.Install(spec)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if plistPath != filepath.Join(launchAgentsDir, ServiceLabel+".plist") {
		t.Fatalf("Install() path = %q", plistPath)
	}
	info, err := os.Stat(plistPath)
	if err != nil {
		t.Fatalf("stat plist: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("plist mode = %v, want 0644", info.Mode().Perm())
	}
	if _, err := os.Stat(spec.LogDir); err != nil {
		t.Fatalf("log directory was not created: %v", err)
	}
	want := []string{
		"bootout gui/501 " + ServiceLabel,
		"bootstrap gui/501 " + plistPath,
	}
	if strings.Join(launchctl.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("launchctl calls = %v, want %v", launchctl.calls, want)
	}
}

func TestServiceManagerInstallIsIdempotent(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	launchctl := &fakeLaunchctl{}
	manager := NewServiceManager(t.TempDir(), launchctl, 501)

	first, err := manager.Install(spec)
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	second, err := manager.Install(spec)
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	if first != second {
		t.Fatalf("Install() returned %q then %q", first, second)
	}
	if len(launchctl.calls) != 4 {
		t.Fatalf("launchctl calls = %v, want bootout and bootstrap twice", launchctl.calls)
	}
}

func TestServiceManagerInstallIgnoresBootoutFailure(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	launchctl := &fakeLaunchctl{bootoutErr: errors.New("Could not find service")}
	manager := NewServiceManager(t.TempDir(), launchctl, 501)

	if _, err := manager.Install(spec); err != nil {
		t.Fatalf("Install() error = %v, want nil for a not-loaded service", err)
	}
	if len(launchctl.calls) != 2 {
		t.Fatalf("launchctl calls = %v, want bootout then bootstrap", launchctl.calls)
	}
}

func TestServiceManagerInstallReportsBootstrapFailure(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	launchctl := &fakeLaunchctl{bootstrapErr: errors.New("Bootstrap failed: 5: Input/output error")}
	manager := NewServiceManager(t.TempDir(), launchctl, 501)

	if _, err := manager.Install(spec); err == nil {
		t.Fatal("Install() succeeded after a failed bootstrap")
	}
}

func TestServiceManagerInstallRejectsInvalidSpec(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	spec.Executable = "state-runner"
	manager := NewServiceManager(t.TempDir(), &fakeLaunchctl{}, 501)

	if _, err := manager.Install(spec); err == nil {
		t.Fatal("Install() accepted a relative executable path")
	}
}

func TestServiceManagerUninstallRemovesTheAgent(t *testing.T) {
	t.Parallel()

	spec := testSpec(t)
	launchctl := &fakeLaunchctl{}
	manager := NewServiceManager(t.TempDir(), launchctl, 501)

	plistPath, err := manager.Install(spec)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	launchctl.calls = nil
	if err := manager.Uninstall(); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if _, err := os.Stat(plistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist still exists after uninstall: %v", err)
	}
	if len(launchctl.calls) != 1 || launchctl.calls[0] != "bootout gui/501 "+ServiceLabel {
		t.Fatalf("launchctl calls = %v, want one bootout", launchctl.calls)
	}
}

func TestServiceManagerUninstallWithoutAgentSucceeds(t *testing.T) {
	t.Parallel()

	launchctl := &fakeLaunchctl{bootoutErr: errors.New("Could not find service")}
	manager := NewServiceManager(t.TempDir(), launchctl, 501)

	if err := manager.Uninstall(); err != nil {
		t.Fatalf("Uninstall() error = %v, want nil for a missing agent", err)
	}
}

func TestServiceManagerStatusReadsLaunchctlState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		output  string
		running bool
	}{
		{name: "running", output: "\tstate = running\n\tpid = 4711\n", running: true},
		{name: "exited", output: "\tstate = exited\n\tlast exit code = 1\n", running: false},
		{name: "waiting", output: "\tstate = waiting\n", running: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			launchctl := &fakeLaunchctl{printOutput: testCase.output}
			manager := NewServiceManager(t.TempDir(), launchctl, 501)

			running, detail, err := manager.Status()
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if running != testCase.running {
				t.Fatalf("Status() running = %v, want %v", running, testCase.running)
			}
			if !strings.Contains(detail, strings.TrimSpace(strings.Split(strings.TrimSpace(testCase.output), "\n")[0])) {
				t.Fatalf("Status() detail = %q, want it to carry the launchctl output", detail)
			}
			if len(launchctl.calls) != 1 || launchctl.calls[0] != "print gui/501 "+ServiceLabel {
				t.Fatalf("launchctl calls = %v, want one print", launchctl.calls)
			}
		})
	}
}

func TestServiceManagerStatusReportsLaunchctlErrors(t *testing.T) {
	t.Parallel()

	launchctl := &fakeLaunchctl{printErr: errors.New("Could not find service")}
	manager := NewServiceManager(t.TempDir(), launchctl, 501)

	running, _, err := manager.Status()
	if err == nil {
		t.Fatal("Status() succeeded for a missing agent")
	}
	if running {
		t.Fatal("Status() reported running for a missing agent")
	}
}
