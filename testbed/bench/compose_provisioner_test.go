package bench_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestComposeAuthorityExporterTargetsIsolatedLinuxWorkload(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64"} {
		t.Run(architecture, func(t *testing.T) {
			helper, _ := filepath.Abs("compose_provisioner.sh")
			root := t.TempDir()
			workspace := filepath.Join(root, "private space")
			bin := filepath.Join(root, "bin")
			for _, directory := range []string{workspace, bin, filepath.Join(root, "server")} {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			capture := filepath.Join(root, "docker-args")
			fakeDocker := `#!/bin/sh
if [ "$1" = image ]; then printf 'linux/%s\n' "$TEST_ARCH"; exit; fi
printf '%s\n' "$@" > "$TEST_CAPTURE"
`
			fakeGo := `#!/bin/sh
test "$GOOS/$GOARCH/$CGO_ENABLED/$GOFLAGS" = "linux/$TEST_ARCH/0/" || exit 71
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; : > "$1"; exit; fi
  shift
done
exit 72
`
			for name, source := range map[string]string{"docker": fakeDocker, "go": fakeGo} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0700); err != nil {
					t.Fatal(err)
				}
			}
			image := "sha256:" + strings.Repeat("a", 64)
			command := exec.CommandContext(t.Context(), "bash", "-c", `set -euo pipefail
source "$1"
launcher="$(prepare_compose_authority_exporter "$2" "$3" "$4")"
"$launcher" '-test.run=^TestCurrentProvisioningExportPublicFixture$' -test.count=1 -test.v
`, "compose-test", helper, root, workspace, image)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_ARCH="+architecture, "TEST_CAPTURE="+capture, "GOFLAGS=-tags=lantern_test_time")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("prepare exporter: %v\n%s", err, output)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pull=never", "--pids-limit=256", "--memory=1g", "-test.timeout=75s", image, "type=bind,source=" + workspace + ",target=" + workspace, filepath.Join(workspace, "authority-linux.test"), "-test.run=^TestCurrentProvisioningExportPublicFixture$"} {
				if !slices.Contains(args, required) {
					t.Fatal("missing isolated exporter argument", required, args)
				}
			}
			var mounts, environments int
			for _, arg := range args {
				if arg == "--mount" {
					mounts++
				}
				if arg == "--env" {
					environments++
				}
			}
			if mounts != 1 || environments != 5 {
				t.Fatal("unexpected exporter access", args)
			}
			if info, err := os.Stat(filepath.Join(workspace, "authority-exporter")); err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("launcher is not private", err)
			}
			// A second preparation must not overwrite the original exporter.
			command = exec.CommandContext(t.Context(), "bash", "-c", `source "$1"; prepare_compose_authority_exporter "$2" "$3" "$4"`, "compose-test", helper, root, workspace, image)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_ARCH="+architecture)
			if err := command.Run(); err == nil {
				t.Fatal("existing provisioning workspace was reused")
			}
		})
	}
}

func TestComposeDeclaredSourceIsNewAndExact(t *testing.T) {
	helper, _ := filepath.Abs("compose_provisioner.sh")
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "refuse-existing"}[existing], func(t *testing.T) {
			root := t.TempDir()
			for _, service := range []string{"lantern-0", "lantern-1", "lantern-2"} {
				if err := os.Mkdir(filepath.Join(root, service), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if existing {
				if err := os.WriteFile(filepath.Join(root, "lantern-2", "ntp.conf"), []byte("original\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.CommandContext(t.Context(), "bash", "-c", `source "$1"; write_compose_time_source "$2"`, "source-test", helper, root)
			output, err := command.CombinedOutput()
			if existing {
				if err == nil {
					t.Fatal("existing source was overwritten")
				}
				if _, err := os.Stat(filepath.Join(root, "lantern-0", "ntp.conf")); !os.IsNotExist(err) {
					t.Fatal("partial source replacement occurred", err)
				}
				if raw, err := os.ReadFile(filepath.Join(root, "lantern-2", "ntp.conf")); err != nil || string(raw) != "original\n" {
					t.Fatal("original source changed", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("write declared source: %v\n%s", err, output)
			}
			for _, service := range []string{"lantern-0", "lantern-1", "lantern-2"} {
				raw, err := os.ReadFile(filepath.Join(root, service, "ntp.conf"))
				if err != nil || string(raw) != "server time.asia.apple.com\n" {
					t.Fatal("wrong declared production source", err)
				}
			}
		})
	}
}

func TestComposeAuthorityExporterRefusesUnsafePreparation(t *testing.T) {
	helper, _ := filepath.Abs("compose_provisioner.sh")
	for _, failure := range []string{"image-ref", "non-linux", "unsupported-arch", "readable-workspace", "symlink-workspace", "existing-exporter"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(root, "private")
			bin := filepath.Join(root, "bin")
			for _, directory := range []string{workspace, bin, filepath.Join(root, "server")} {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			platform := "linux/arm64"
			image := "sha256:" + strings.Repeat("a", 64)
			switch failure {
			case "image-ref":
				image = "lantern:latest"
			case "non-linux":
				platform = "darwin/arm64"
			case "unsupported-arch":
				platform = "linux/arm/v7"
			case "readable-workspace":
				if err := os.Chmod(workspace, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink-workspace":
				link := filepath.Join(root, "link")
				if err := os.Symlink(workspace, link); err != nil {
					t.Fatal(err)
				}
				workspace = link
			case "existing-exporter":
				if err := os.WriteFile(filepath.Join(workspace, "authority-linux.test"), []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for name, source := range map[string]string{
				"docker": "#!/bin/sh\nprintf '%s\\n' \"$TEST_PLATFORM\"\n",
				"go":     "#!/bin/sh\n: > \"$TEST_MARKER\"\nexit 98\n",
			} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0700); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(root, "compiled")
			command := exec.CommandContext(t.Context(), "bash", "-c", `source "$1"; prepare_compose_authority_exporter "$2" "$3" "$4"`, "denied-test", helper, root, workspace, image)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_PLATFORM="+platform, "TEST_MARKER="+marker)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("unsafe preparation accepted: %s", output)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("unsafe preparation reached compilation", err)
			}
			if failure == "existing-exporter" {
				if raw, err := os.ReadFile(filepath.Join(workspace, "authority-linux.test")); err != nil || string(raw) != "original" {
					t.Fatal("original exporter changed", err)
				}
			}
		})
	}
}
