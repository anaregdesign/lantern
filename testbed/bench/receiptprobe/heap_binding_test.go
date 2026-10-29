package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/benchheap"
)

// Synthetic frozen host-bind schema fixture, not runtime evidence.
const retainedMountFixtureFiles = `{"baseline.after.daemon":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n","baseline.after.inspect.json":"{\"cmd\":null,\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-1\",\"oneoff\":\"False\",\"owner\":\"\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":101,\"restarting\":false,\"running\":true,\"started_at\":\"2026-10-30T00:00:01Z\",\"status\":\"running\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","baseline.after.json":"{\"daemon_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.after.daemon\",\"sha256\":\"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547\"},\"host_identity\":{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501},\"inspect_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.after.inspect.json\",\"sha256\":\"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226\"},\"phase\":\"baseline\",\"position\":\"after\",\"protocol\":\"symmetric_post_gc_2_v1\",\"schema\":1,\"service\":\"lantern-0\"}\n","baseline.before.daemon":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n","baseline.before.inspect.json":"{\"cmd\":null,\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-1\",\"oneoff\":\"False\",\"owner\":\"\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":101,\"restarting\":false,\"running\":true,\"started_at\":\"2026-10-30T00:00:01Z\",\"status\":\"running\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","baseline.before.json":"{\"daemon_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.before.daemon\",\"sha256\":\"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547\"},\"host_identity\":{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501},\"inspect_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.before.inspect.json\",\"sha256\":\"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226\"},\"phase\":\"baseline\",\"position\":\"before\",\"protocol\":\"symmetric_post_gc_2_v1\",\"schema\":1,\"service\":\"lantern-0\"}\n","binding.json":"{\"canonical_directory\":\"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"clone_after_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/clone-after.json\",\"sha256\":\"54a03770a2d0e7a52d7a083ff281c7cf89ee93bf5ac7b07cb67085cbfe19a02e\"},\"clone_before_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/clone-before.json\",\"sha256\":\"d6a9dc6294c9bdc3ec9dc82a410499376be2433641a745c3beb47d6c4fc355c3\"},\"context\":{\"cpuset\":\"0-3\",\"image_id\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"run_id\":\"fixture-case\",\"runtime_user\":\"501:20\",\"scenario_sha256\":\"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\",\"source_commit\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"source_tree\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"},\"daemon_after_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/daemon-after\",\"sha256\":\"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547\"},\"daemon_before_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/daemon-before\",\"sha256\":\"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547\"},\"daemon_id\":\"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\",\"host_after_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/host-after.json\",\"sha256\":\"3b79133e182fef6939ea1a7432264f69aba81fb1763216e07d2dfeafb61809f1\"},\"host_before_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/host-before.json\",\"sha256\":\"3b79133e182fef6939ea1a7432264f69aba81fb1763216e07d2dfeafb61809f1\"},\"host_precreate_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/host-precreate.json\",\"sha256\":\"3b79133e182fef6939ea1a7432264f69aba81fb1763216e07d2dfeafb61809f1\"},\"original_after_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/original-after.json\",\"sha256\":\"3b651c388fc89428632f6ee71f7fba160dcfdc513bd70b244b3349efd207d30a\"},\"original_before_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/original-before.json\",\"sha256\":\"3b651c388fc89428632f6ee71f7fba160dcfdc513bd70b244b3349efd207d30a\"},\"pairs_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/requested-pairs.json\",\"sha256\":\"dcc9dbd442a1de52188cef2de66ede9e71e8a0e5eaa2d87ec7608dd41a9a33ab\"},\"probe\":{\"completed_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-completed.json\",\"sha256\":\"771dc08312ebfe1737aa3f5594c9e1807b08c18e71264701eb3d1bb118bbf35c\"},\"created_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-created.json\",\"sha256\":\"ad27181454415ec6dd94a7eb8708526c0e4ac31319d322e99c67b28a1cdb6de2\"},\"environment_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/image-environment.json\",\"sha256\":\"daffc427a7a56194f63925afe4cbb527bca0a1791249f66b76619f175845ddad\"},\"output_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-output.log\",\"sha256\":\"d8d3b3253daf4da87ba154ed67b7165d015c679d0395409f0582fee85459f96e\"},\"remove_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-remove.log\",\"sha256\":\"1ac99b9c00c8778c176ccbd6ec15b14bc1f5886844d2919edb429deb5db1430f\"},\"wait_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-wait.log\",\"sha256\":\"9a271f2a916b0b6ee6cecb2426f0b3206ef074578be55d9bc94f6f3fe3ab86aa\"}},\"protocol\":\"symmetric_post_gc_2_v1\",\"schema\":1,\"service\":\"lantern-0\",\"session\":{\"binary_sha256\":\"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\",\"container_id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image_id\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"protocol\":\"symmetric_post_gc_2_v1\",\"run_nonce\":\"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\",\"schema\":1,\"source_commit\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"source_tree\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"uid\":501}}\n","clone-after.json":"{\"cmd\":[\"init-security\"],\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"2222222222222222222222222222222222222222222222222222222222222222\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"lantern-owned-init.DEF456\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-security-init\",\"oneoff\":\"True\",\"owner\":\"compose-security-initializer\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":0,\"restarting\":false,\"running\":false,\"started_at\":\"2026-10-30T00:00:00Z\",\"status\":\"exited\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","clone-before.json":"{\"cmd\":[\"init-security\"],\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"2222222222222222222222222222222222222222222222222222222222222222\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"lantern-owned-init.DEF456\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-security-init\",\"oneoff\":\"True\",\"owner\":\"compose-security-initializer\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":0,\"restarting\":false,\"running\":false,\"started_at\":\"0001-01-01T00:00:00Z\",\"status\":\"created\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","daemon-after":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n","daemon-before":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n","host-after.json":"{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501}\n","host-before.json":"{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501}\n","host-precreate.json":"{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501}\n","image-environment.json":"[\"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\",\"LANTERN_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"]\n","original-after.json":"{\"cmd\":null,\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-1\",\"oneoff\":\"False\",\"owner\":\"\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":0,\"restarting\":false,\"running\":false,\"started_at\":\"0001-01-01T00:00:00Z\",\"status\":\"created\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","original-before.json":"{\"cmd\":null,\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-1\",\"oneoff\":\"False\",\"owner\":\"\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":0,\"restarting\":false,\"running\":false,\"started_at\":\"0001-01-01T00:00:00Z\",\"status\":\"created\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","post.after.daemon":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n","post.after.inspect.json":"{\"cmd\":null,\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-1\",\"oneoff\":\"False\",\"owner\":\"\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":101,\"restarting\":false,\"running\":true,\"started_at\":\"2026-10-30T00:00:01Z\",\"status\":\"running\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","post.after.json":"{\"daemon_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.after.daemon\",\"sha256\":\"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547\"},\"host_identity\":{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501},\"inspect_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.after.inspect.json\",\"sha256\":\"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226\"},\"phase\":\"post\",\"position\":\"after\",\"protocol\":\"symmetric_post_gc_2_v1\",\"schema\":1,\"service\":\"lantern-0\"}\n","post.before.daemon":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n","post.before.inspect.json":"{\"cmd\":null,\"entrypoint\":[\"/app/lantern\"],\"host_config\":{\"auto_remove\":false,\"binds\":[\"/private/fake-config:/run/lantern-config:ro\",\"/private/fake-config/ntp.conf:/etc/ntp.conf:ro\"],\"cap_add\":null,\"cap_drop\":null,\"ipc_mode\":\"private\",\"mounts\":[{\"Source\":\"fixture_state-0\",\"Target\":\"/state\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"Source\":\"fixture_backup-0\",\"Target\":\"/data\",\"Type\":\"volume\",\"VolumeOptions\":{\"NoCopy\":true}},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/run/lantern-bench-heap\",\"Type\":\"bind\"}],\"network_mode\":\"fixture_default\",\"pid_mode\":\"\",\"privileged\":false,\"readonly_rootfs\":false,\"restart_policy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"security_opt\":null,\"tmpfs\":null,\"userns_mode\":\"\",\"uts_mode\":\"\"},\"id\":\"1111111111111111111111111111111111111111111111111111111111111111\",\"image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"initializer\":\"\",\"kind\":\"heap-mount-observation\",\"mounts\":[{\"Destination\":\"/run/lantern-config\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config\",\"Type\":\"bind\"},{\"Destination\":\"/etc/ntp.conf\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/private/fake-config/ntp.conf\",\"Type\":\"bind\"},{\"Destination\":\"/state\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_state-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_state-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/data\",\"Driver\":\"local\",\"Mode\":\"z\",\"Name\":\"fixture_backup-0\",\"Propagation\":\"\",\"RW\":true,\"Source\":\"/var/lib/docker/volumes/fixture_backup-0/_data\",\"Type\":\"volume\"},{\"Destination\":\"/run/lantern-bench-heap\",\"Mode\":\"\",\"Propagation\":\"rprivate\",\"RW\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"name\":\"/fixture-case-lantern-0-1\",\"oneoff\":\"False\",\"owner\":\"\",\"project\":\"fixture-case\",\"restart_count\":0,\"schema\":1,\"service\":\"lantern-0\",\"state\":{\"exit_code\":0,\"oom_killed\":false,\"paused\":false,\"pid\":101,\"restarting\":false,\"running\":true,\"started_at\":\"2026-10-30T00:00:01Z\",\"status\":\"running\"},\"user\":\"501:20\",\"working_dir\":\"/app\"}\n","post.before.json":"{\"daemon_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.before.daemon\",\"sha256\":\"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547\"},\"host_identity\":{\"device\":\"1\",\"gid\":20,\"inode\":\"100\",\"mode\":448,\"type\":\"directory\",\"uid\":501},\"inspect_file\":{\"path\":\"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.before.inspect.json\",\"sha256\":\"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226\"},\"phase\":\"post\",\"position\":\"before\",\"protocol\":\"symmetric_post_gc_2_v1\",\"schema\":1,\"service\":\"lantern-0\"}\n","probe-completed.json":"{\"Config\":{\"Cmd\":[\"stat\",\"-c\",\"%d:%i:%F\",\"/proof/original-0\",\"/proof/clone-0\"],\"Entrypoint\":[\"/bin/busybox\"],\"Env\":[\"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\",\"LANTERN_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"],\"Image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"Labels\":{\"com.docker.compose.oneoff\":\"True\",\"com.docker.compose.project\":\"fixture-case\",\"com.docker.compose.service\":\"lantern-0-bind-proof\",\"lantern.initializer\":\"lantern-owned-init.ABC123\",\"lantern.owner\":\"compose-bind-identity-probe\"},\"User\":\"501:20\"},\"HostConfig\":{\"AutoRemove\":false,\"Binds\":null,\"CapAdd\":null,\"CapDrop\":[\"ALL\"],\"DeviceCgroupRules\":null,\"DeviceRequests\":null,\"Devices\":null,\"Memory\":33554432,\"Mounts\":[{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":true,\"Source\":\"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/proof/original-0\",\"Type\":\"bind\"},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/proof/clone-0\",\"Type\":\"bind\"}],\"NanoCpus\":250000000,\"NetworkMode\":\"none\",\"PidMode\":\"\",\"PidsLimit\":16,\"PortBindings\":null,\"Privileged\":false,\"PublishAllPorts\":false,\"ReadonlyRootfs\":true,\"RestartPolicy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"SecurityOpt\":[\"no-new-privileges:true\"],\"Tmpfs\":null,\"UTSMode\":\"\",\"UsernsMode\":\"\",\"VolumesFrom\":null},\"Id\":\"3333333333333333333333333333333333333333333333333333333333333333\",\"Image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"Mounts\":[{\"Destination\":\"/proof/original-0\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"},{\"Destination\":\"/proof/clone-0\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"Name\":\"/fixture-case-lantern-0-bind-proof\",\"RestartCount\":0,\"State\":{\"ExitCode\":0,\"OOMKilled\":false,\"Running\":false,\"StartedAt\":\"2026-10-30T00:00:00Z\",\"Status\":\"exited\"}}\n","probe-created.json":"{\"Config\":{\"Cmd\":[\"stat\",\"-c\",\"%d:%i:%F\",\"/proof/original-0\",\"/proof/clone-0\"],\"Entrypoint\":[\"/bin/busybox\"],\"Env\":[\"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\",\"LANTERN_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"],\"Image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"Labels\":{\"com.docker.compose.oneoff\":\"True\",\"com.docker.compose.project\":\"fixture-case\",\"com.docker.compose.service\":\"lantern-0-bind-proof\",\"lantern.initializer\":\"lantern-owned-init.ABC123\",\"lantern.owner\":\"compose-bind-identity-probe\"},\"User\":\"501:20\"},\"HostConfig\":{\"AutoRemove\":false,\"Binds\":null,\"CapAdd\":null,\"CapDrop\":[\"ALL\"],\"DeviceCgroupRules\":null,\"DeviceRequests\":null,\"Devices\":null,\"Memory\":33554432,\"Mounts\":[{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":true,\"Source\":\"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/proof/original-0\",\"Type\":\"bind\"},{\"BindOptions\":{\"Propagation\":\"rprivate\"},\"ReadOnly\":true,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Target\":\"/proof/clone-0\",\"Type\":\"bind\"}],\"NanoCpus\":250000000,\"NetworkMode\":\"none\",\"PidMode\":\"\",\"PidsLimit\":16,\"PortBindings\":null,\"Privileged\":false,\"PublishAllPorts\":false,\"ReadonlyRootfs\":true,\"RestartPolicy\":{\"MaximumRetryCount\":0,\"Name\":\"no\"},\"SecurityOpt\":[\"no-new-privileges:true\"],\"Tmpfs\":null,\"UTSMode\":\"\",\"UsernsMode\":\"\",\"VolumesFrom\":null},\"Id\":\"3333333333333333333333333333333333333333333333333333333333333333\",\"Image\":\"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"Mounts\":[{\"Destination\":\"/proof/original-0\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"},{\"Destination\":\"/proof/clone-0\",\"Mode\":\"ro\",\"Propagation\":\"rprivate\",\"RW\":false,\"Source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"Type\":\"bind\"}],\"Name\":\"/fixture-case-lantern-0-bind-proof\",\"RestartCount\":0,\"State\":{\"ExitCode\":0,\"OOMKilled\":false,\"Running\":false,\"StartedAt\":\"0001-01-01T00:00:00Z\",\"Status\":\"created\"}}\n","probe-output.log":"9:123:directory\n9:123:directory\n","probe-remove.log":"3333333333333333333333333333333333333333333333333333333333333333\n","probe-wait.log":"0\n","requested-pairs.json":"[{\"clone_source\":\"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"declared_source\":\"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\",\"destination\":\"/run/lantern-bench-heap\",\"expected_type\":\"directory\",\"original_source\":\"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0\"}]\n"}`

func retainedMountFixture(t *testing.T, root, phase string, index int, ctx retainedHeapContext, session benchheap.Session) (retainedHeapReference, retainedHeapReference, retainedHeapReference) {
	t.Helper()
	var source map[string]string
	if err := json.Unmarshal([]byte(retainedMountFixtureFiles), &source); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "heap-mount-evidence", fmt.Sprintf("lantern-%d", index))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]any{}
	for name, data := range source {
		for _, item := range [][2]string{{strings.Repeat("1", 64), "ORIGINALCID"}, {strings.Repeat("2", 64), "CLONECID"}, {strings.Repeat("3", 64), "PROBECID"}} {
			data = strings.ReplaceAll(data, item[0], item[1])
		}
		for _, item := range [][2]string{{"/synthetic/heap-bind-fixture", root}, {"lantern-0", fmt.Sprintf("lantern-%d", index)}, {"fixture-case", ctx.RunID}, {"ORIGINALCID", session.ContainerID}, {"CLONECID", fmt.Sprintf("%064x", index+101)}, {"PROBECID", fmt.Sprintf("%064x", index+201)}, {"sha256:" + strings.Repeat("c", 64), ctx.ImageID}} {
			data = strings.ReplaceAll(data, item[0], item[1])
		}
		// Raw daemon/stat/wait/remove output must retain its bytes, including
		// the trailing LF of wait's JSON-looking scalar "0\n".
		var value any = data
		if strings.HasSuffix(name, ".json") {
			if err := json.Unmarshal([]byte(data), &value); err != nil {
				t.Fatal(err)
			}
		}
		files[name] = value
	}
	for name, value := range files {
		if name == "host-precreate.json" || name == "host-before.json" || name == "host-after.json" {
			value.(map[string]any)["inode"] = fmt.Sprint(100 + index)
		} else if v, ok := value.(map[string]any); ok && v["host_identity"] != nil {
			v["host_identity"].(map[string]any)["inode"] = fmt.Sprint(100 + index)
		}
	}
	binding := files["binding.json"].(map[string]any)
	raw, _ := json.Marshal(ctx)
	var context map[string]any
	_ = json.Unmarshal(raw, &context)
	delete(context, "workload")
	binding["context"] = context
	binding["session"] = session
	encoded := map[string][]byte{}
	var encode func(string) []byte
	var refresh func(any)
	refresh = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if len(v) == 2 && v["path"] != nil && v["sha256"] != nil {
				target := filepath.Base(v["path"].(string))
				data := encode(target)
				v["sha256"] = retainedReference("", data).SHA256
			} else {
				for _, child := range v {
					refresh(child)
				}
			}
		case []any:
			for _, child := range v {
				refresh(child)
			}
		}
	}
	encode = func(name string) []byte {
		if data, ok := encoded[name]; ok {
			return data
		}
		value, ok := files[name]
		if !ok {
			t.Fatal("missing synthetic mount fixture", name)
		}
		refresh(value)
		var data []byte
		if s, ok := value.(string); ok {
			data = []byte(s)
		} else {
			data, _ = json.Marshal(value)
		}
		encoded[name] = data
		return data
	}
	for name := range files {
		if err := os.WriteFile(filepath.Join(base, name), encode(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ref := func(name string) retainedHeapReference {
		return retainedReference(filepath.Join(base, name), encoded[name])
	}
	return ref("binding.json"), ref(phase + ".before.json"), ref(phase + ".after.json")
}

func refreshRetainedMountRefs(t *testing.T, root string) {
	t.Helper()
	for index := 0; index < 3; index++ {
		base := filepath.Join(root, "heap-mount-evidence", fmt.Sprintf("lantern-%d", index))
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]any{}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(base, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var value any = string(data)
			if strings.HasSuffix(entry.Name(), ".json") {
				if err := json.Unmarshal(data, &value); err != nil {
					t.Fatal(err)
				}
			}
			files[entry.Name()] = value
		}
		encoded := map[string][]byte{}
		var encode func(string) []byte
		var refresh func(any)
		refresh = func(v any) {
			switch value := v.(type) {
			case map[string]any:
				if len(value) == 2 && value["path"] != nil && value["sha256"] != nil {
					value["sha256"] = retainedReference("", encode(filepath.Base(value["path"].(string)))).SHA256
				} else {
					for _, child := range value {
						refresh(child)
					}
				}
			case []any:
				for _, child := range value {
					refresh(child)
				}
			}
		}
		encode = func(name string) []byte {
			if data, ok := encoded[name]; ok {
				return data
			}
			v := files[name]
			refresh(v)
			var data []byte
			if s, ok := v.(string); ok {
				data = []byte(s)
			} else {
				data, _ = json.Marshal(v)
			}
			encoded[name] = data
			return data
		}
		for name := range files {
			if err := os.WriteFile(filepath.Join(base, name), encode(name), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, phase := range []string{"baseline", "post"} {
			suffix := "pre"
			if phase == "post" {
				suffix = "post"
			}
			path := filepath.Join(root, "retained_heap_"+suffix+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cut retainedHeapCut
			if json.Unmarshal(data, &cut) != nil {
				t.Fatal("fixture cut")
			}
			node := &cut.Replicas[index]
			node.MountBindingFile = retainedReference(filepath.Join(base, "binding.json"), encoded["binding.json"])
			node.MountInspectBeforeFile = retainedReference(filepath.Join(base, phase+".before.json"), encoded[phase+".before.json"])
			node.MountInspectAfterFile = retainedReference(filepath.Join(base, phase+".after.json"), encoded[phase+".after.json"])
			mustWriteReceiptArtifact(t, path, cut)
		}
	}
}

func TestRetainedHeapMountProofRequiresRawIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file string
		damage     func(map[string]any)
	}{
		{"host replacement", "host-after.json", func(v map[string]any) { v["inode"] = "999" }},
		{"foreign daemon", "binding.json", func(v map[string]any) { v["daemon_id"] = "foreign-daemon" }},
		{"producer flag", "binding.json", func(v map[string]any) { v["status"] = "PASS" }},
		{"missing mapped proof", "binding.json", func(v map[string]any) { v["probe"] = nil }},
		{"clone unknown Source", "clone-before.json", func(v map[string]any) { v["mounts"].([]any)[4].(map[string]any)["Source"] = "/unproved" }},
		{"clone options", "clone-before.json", func(v map[string]any) {
			v["host_config"].(map[string]any)["mounts"].([]any)[2].(map[string]any)["BindOptions"].(map[string]any)["Propagation"] = "shared"
		}},
		{"NoCopy", "original-before.json", func(v map[string]any) {
			v["host_config"].(map[string]any)["mounts"].([]any)[0].(map[string]any)["VolumeOptions"].(map[string]any)["NoCopy"] = false
		}},
		{"secret projection", "original-before.json", func(v map[string]any) { v["Env"] = []string{"TOKEN=secret"} }},
		{"serving process changed", "post.before.inspect.json", func(v map[string]any) { v["state"].(map[string]any)["pid"] = 9999 }},
		{"probe network", "probe-created.json", func(v map[string]any) { v["HostConfig"].(map[string]any)["NetworkMode"] = "host" }},
		{"probe third Source", "probe-created.json", func(v map[string]any) { v["Mounts"].([]any)[0].(map[string]any)["Source"] = "/third-path" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre, post, endpoints := retainedHeapFixture(t)
			root := filepath.Dir(pre)
			path := filepath.Join(root, "heap-mount-evidence/lantern-0", tc.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if json.Unmarshal(data, &value) != nil {
				t.Fatal("fixture mount")
			}
			tc.damage(value)
			mustWriteReceiptArtifact(t, path, value)
			refreshRetainedMountRefs(t, root)
			report := receiptLeakReport{SnapshotProtocol: benchheap.Protocol, Replicas: endpoints, NaturalChecksVerdict: "pass", NaturalPostHeapObservationVerdict: "pass", PairRequired: true, OverallMemoryVerdict: "requires_pair"}
			evaluateRetainedHeap(&report, root, pre, post, 32)
			if report.RetainedVerdict != "incomplete" {
				t.Fatalf("forged mount accepted: %+v", report)
			}
		})
	}
}

func TestRetainedHeapMountLiteralAndFrozenCloneSource(t *testing.T) {
	t.Parallel()
	for _, literal := range []bool{false, true} {
		t.Run(fmt.Sprint(literal), func(t *testing.T) {
			pre, post, endpoints := retainedHeapFixture(t)
			root := filepath.Dir(pre)
			for i := 0; i < 3; i++ {
				base := filepath.Join(root, "heap-mount-evidence", fmt.Sprintf("lantern-%d", i))
				names := []string{"clone-before.json", "clone-after.json"}
				if literal {
					names = append(names, "original-before.json", "original-after.json", "baseline.before.inspect.json", "baseline.after.inspect.json", "post.before.inspect.json", "post.after.inspect.json")
				}
				for _, name := range names {
					path := filepath.Join(base, name)
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var v map[string]any
					if json.Unmarshal(data, &v) != nil {
						t.Fatal("fixture")
					}
					canonical := filepath.Join(root, "heap-control-evidence", fmt.Sprintf("lantern-%d", i))
					v["host_config"].(map[string]any)["mounts"].([]any)[2].(map[string]any)["Source"] = canonical
					v["mounts"].([]any)[4].(map[string]any)["Source"] = canonical
					mustWriteReceiptArtifact(t, path, v)
				}
				if literal {
					path := filepath.Join(base, "binding.json")
					data, _ := os.ReadFile(path)
					var v map[string]any
					_ = json.Unmarshal(data, &v)
					v["probe"] = nil
					mustWriteReceiptArtifact(t, path, v)
					mustWriteReceiptArtifact(t, filepath.Join(base, "requested-pairs.json"), []any{})
				}
			}
			refreshRetainedMountRefs(t, root)
			report := receiptLeakReport{SnapshotProtocol: benchheap.Protocol, Replicas: endpoints, NaturalChecksVerdict: "pass", NaturalPostHeapObservationVerdict: "pass", PairRequired: true, OverallMemoryVerdict: "requires_pair"}
			evaluateRetainedHeap(&report, root, pre, post, 32)
			if report.RetainedVerdict != "pass" {
				t.Fatalf("valid original-first proof rejected: %+v", report)
			}
		})
	}
}

func TestRetainedHeapMountRawPathsAndMissingEvidenceRefuse(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "symlink", "cross-node", "escaped"} {
		t.Run(kind, func(t *testing.T) {
			pre, post, endpoints := retainedHeapFixture(t)
			root := filepath.Dir(pre)
			path := filepath.Join(root, "heap-mount-evidence/lantern-0/probe-output.log")
			if kind == "missing" || kind == "symlink" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink("probe-wait.log", path); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				data, _ := os.ReadFile(post)
				var cut retainedHeapCut
				_ = json.Unmarshal(data, &cut)
				if kind == "cross-node" {
					cut.Replicas[0].MountBindingFile = cut.Replicas[1].MountBindingFile
				} else {
					cut.Replicas[0].MountBindingFile.Path = filepath.Join(root, "..", "binding.json")
				}
				mustWriteReceiptArtifact(t, post, cut)
			}
			report := receiptLeakReport{SnapshotProtocol: benchheap.Protocol, Replicas: endpoints, NaturalChecksVerdict: "pass", NaturalPostHeapObservationVerdict: "pass", PairRequired: true, OverallMemoryVerdict: "requires_pair"}
			evaluateRetainedHeap(&report, root, pre, post, 32)
			if report.RetainedVerdict != "incomplete" {
				t.Fatalf("unsafe bind proof accepted: %+v", report)
			}
		})
	}
}

func TestRetainedHeapMountProbeOomDefaultEquivalenceIsTypedAndFieldScoped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		before, after any
		accepted      bool
	}{
		{"null-false", nil, false, true}, {"missing-false", "missing", false, true}, {"false-null", false, nil, true},
		{"false-true", false, true, false}, {"true-false", true, false, false}, {"false-int", false, 0, false},
		{"both-int", 0, 0, false}, {"both-true", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre, post, endpoints := retainedHeapFixture(t)
			root := filepath.Dir(pre)
			base := filepath.Join(root, "heap-mount-evidence/lantern-0")
			for _, change := range []struct {
				name  string
				value any
			}{{"probe-created.json", tc.before}, {"probe-completed.json", tc.after}} {
				path := filepath.Join(base, change.name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var row map[string]any
				if json.Unmarshal(data, &row) != nil {
					t.Fatal("synthetic probe")
				}
				host := row["HostConfig"].(map[string]any)
				if change.value == "missing" {
					delete(host, "OomKillDisable")
				} else {
					host["OomKillDisable"] = change.value
				}
				mustWriteReceiptArtifact(t, path, row)
			}
			refreshRetainedMountRefs(t, root)
			report := receiptLeakReport{SnapshotProtocol: benchheap.Protocol, Replicas: endpoints, NaturalChecksVerdict: "pass", NaturalPostHeapObservationVerdict: "pass", PairRequired: true, OverallMemoryVerdict: "requires_pair"}
			evaluateRetainedHeap(&report, root, pre, post, 32)
			if (report.RetainedVerdict == "pass") != tc.accepted {
				t.Fatalf("OOM default comparison changed scope: %+v", report)
			}
		})
	}
}
