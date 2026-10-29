import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import retained_heap_pair as pair


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, allow_nan=False))
    path.chmod(0o600)
    return {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def memory(cycle, forced, heap=1000):
    return {"heap_alloc": heap, "heap_objects": 50, "heap_inuse": heap + 100,
            "num_gc": cycle, "num_forced_gc": forced, "last_gc": cycle * 1000,
            "next_gc": heap * 2, "marked_live": heap, "metric_cycles": cycle,
            "metric_forced": forced, "cycle_after": cycle}


def native(count):
    idle = {"begun": count, "completed": count, "active": 0}
    return {"generation": 1, "faulted": False, "pending_count": 0, "pending_bytes": 0,
            "pending_local": False, "snapshot_active": False,
            "origins": [{"id": "origin-a", "sequence": count, "wall_ns": 100, "logical": 0}],
            "log_sequence": count + 22, "log_length": count + 22, "log_capacity": 10000,
            "log_evicted": 0, "receipt_entries": count, "receipt_bytes": count * 200,
            "receipt_high_water_ms": count, "receipt_oldest_deadline_ms": count + 10000,
            "vertices": count, "edges": 0,
            "public": idle, "peer_unary": copy.deepcopy(idle), "apply": copy.deepcopy(idle),
            "peer_subscribe": {"begun": 2, "completed": 0, "active": 2}}


# Frozen synthetic mapped schema fixture; no runtime or qualification evidence.
_MOUNT_FIXTURE_FILES = {'baseline.after.daemon': 'bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n', 'baseline.after.inspect.json': '{"cmd":null,"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"1111111111111111111111111111111111111111111111111111111111111111","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-1","oneoff":"False","owner":"","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":101,"restarting":false,"running":true,"started_at":"2026-10-30T00:00:01Z","status":"running"},"user":"501:20","working_dir":"/app"}\n', 'baseline.after.json': '{"daemon_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.after.daemon","sha256":"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547"},"host_identity":{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501},"inspect_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.after.inspect.json","sha256":"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226"},"phase":"baseline","position":"after","protocol":"symmetric_post_gc_2_v1","schema":1,"service":"lantern-0"}\n', 'baseline.before.daemon': 'bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n', 'baseline.before.inspect.json': '{"cmd":null,"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"1111111111111111111111111111111111111111111111111111111111111111","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-1","oneoff":"False","owner":"","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":101,"restarting":false,"running":true,"started_at":"2026-10-30T00:00:01Z","status":"running"},"user":"501:20","working_dir":"/app"}\n', 'baseline.before.json': '{"daemon_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.before.daemon","sha256":"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547"},"host_identity":{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501},"inspect_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/baseline.before.inspect.json","sha256":"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226"},"phase":"baseline","position":"before","protocol":"symmetric_post_gc_2_v1","schema":1,"service":"lantern-0"}\n', 'binding.json': '{"canonical_directory":"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","clone_after_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/clone-after.json","sha256":"54a03770a2d0e7a52d7a083ff281c7cf89ee93bf5ac7b07cb67085cbfe19a02e"},"clone_before_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/clone-before.json","sha256":"d6a9dc6294c9bdc3ec9dc82a410499376be2433641a745c3beb47d6c4fc355c3"},"context":{"cpuset":"0-3","image_id":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","run_id":"fixture-case","runtime_user":"501:20","scenario_sha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","source_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source_tree":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"daemon_after_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/daemon-after","sha256":"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547"},"daemon_before_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/daemon-before","sha256":"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547"},"daemon_id":"bdca5e76-2e60-4ca0-bdf8-6756eef8616f","host_after_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/host-after.json","sha256":"3b79133e182fef6939ea1a7432264f69aba81fb1763216e07d2dfeafb61809f1"},"host_before_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/host-before.json","sha256":"3b79133e182fef6939ea1a7432264f69aba81fb1763216e07d2dfeafb61809f1"},"host_precreate_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/host-precreate.json","sha256":"3b79133e182fef6939ea1a7432264f69aba81fb1763216e07d2dfeafb61809f1"},"original_after_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/original-after.json","sha256":"3b651c388fc89428632f6ee71f7fba160dcfdc513bd70b244b3349efd207d30a"},"original_before_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/original-before.json","sha256":"3b651c388fc89428632f6ee71f7fba160dcfdc513bd70b244b3349efd207d30a"},"pairs_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/requested-pairs.json","sha256":"dcc9dbd442a1de52188cef2de66ede9e71e8a0e5eaa2d87ec7608dd41a9a33ab"},"probe":{"completed_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-completed.json","sha256":"771dc08312ebfe1737aa3f5594c9e1807b08c18e71264701eb3d1bb118bbf35c"},"created_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-created.json","sha256":"ad27181454415ec6dd94a7eb8708526c0e4ac31319d322e99c67b28a1cdb6de2"},"environment_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/image-environment.json","sha256":"daffc427a7a56194f63925afe4cbb527bca0a1791249f66b76619f175845ddad"},"output_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-output.log","sha256":"d8d3b3253daf4da87ba154ed67b7165d015c679d0395409f0582fee85459f96e"},"remove_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-remove.log","sha256":"1ac99b9c00c8778c176ccbd6ec15b14bc1f5886844d2919edb429deb5db1430f"},"wait_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/probe-wait.log","sha256":"9a271f2a916b0b6ee6cecb2426f0b3206ef074578be55d9bc94f6f3fe3ab86aa"}},"protocol":"symmetric_post_gc_2_v1","schema":1,"service":"lantern-0","session":{"binary_sha256":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff","container_id":"1111111111111111111111111111111111111111111111111111111111111111","image_id":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","protocol":"symmetric_post_gc_2_v1","run_nonce":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","schema":1,"source_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source_tree":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","uid":501}}\n', 'clone-after.json': '{"cmd":["init-security"],"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"2222222222222222222222222222222222222222222222222222222222222222","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"lantern-owned-init.DEF456","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-security-init","oneoff":"True","owner":"compose-security-initializer","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":0,"restarting":false,"running":false,"started_at":"2026-10-30T00:00:00Z","status":"exited"},"user":"501:20","working_dir":"/app"}\n', 'clone-before.json': '{"cmd":["init-security"],"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"2222222222222222222222222222222222222222222222222222222222222222","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"lantern-owned-init.DEF456","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-security-init","oneoff":"True","owner":"compose-security-initializer","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":0,"restarting":false,"running":false,"started_at":"0001-01-01T00:00:00Z","status":"created"},"user":"501:20","working_dir":"/app"}\n', 'daemon-after': 'bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n', 'daemon-before': 'bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n', 'host-after.json': '{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501}\n', 'host-before.json': '{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501}\n', 'host-precreate.json': '{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501}\n', 'image-environment.json': '["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","LANTERN_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]\n', 'original-after.json': '{"cmd":null,"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"1111111111111111111111111111111111111111111111111111111111111111","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-1","oneoff":"False","owner":"","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":0,"restarting":false,"running":false,"started_at":"0001-01-01T00:00:00Z","status":"created"},"user":"501:20","working_dir":"/app"}\n', 'original-before.json': '{"cmd":null,"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"1111111111111111111111111111111111111111111111111111111111111111","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-1","oneoff":"False","owner":"","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":0,"restarting":false,"running":false,"started_at":"0001-01-01T00:00:00Z","status":"created"},"user":"501:20","working_dir":"/app"}\n', 'post.after.daemon': 'bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n', 'post.after.inspect.json': '{"cmd":null,"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"1111111111111111111111111111111111111111111111111111111111111111","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-1","oneoff":"False","owner":"","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":101,"restarting":false,"running":true,"started_at":"2026-10-30T00:00:01Z","status":"running"},"user":"501:20","working_dir":"/app"}\n', 'post.after.json': '{"daemon_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.after.daemon","sha256":"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547"},"host_identity":{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501},"inspect_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.after.inspect.json","sha256":"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226"},"phase":"post","position":"after","protocol":"symmetric_post_gc_2_v1","schema":1,"service":"lantern-0"}\n', 'post.before.daemon': 'bdca5e76-2e60-4ca0-bdf8-6756eef8616f\n', 'post.before.inspect.json': '{"cmd":null,"entrypoint":["/app/lantern"],"host_config":{"auto_remove":false,"binds":["/private/fake-config:/run/lantern-config:ro","/private/fake-config/ntp.conf:/etc/ntp.conf:ro"],"cap_add":null,"cap_drop":null,"ipc_mode":"private","mounts":[{"Source":"fixture_state-0","Target":"/state","Type":"volume","VolumeOptions":{"NoCopy":true}},{"Source":"fixture_backup-0","Target":"/data","Type":"volume","VolumeOptions":{"NoCopy":true}},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/run/lantern-bench-heap","Type":"bind"}],"network_mode":"fixture_default","pid_mode":"","privileged":false,"readonly_rootfs":false,"restart_policy":{"MaximumRetryCount":0,"Name":"no"},"security_opt":null,"tmpfs":null,"userns_mode":"","uts_mode":""},"id":"1111111111111111111111111111111111111111111111111111111111111111","image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","initializer":"","kind":"heap-mount-observation","mounts":[{"Destination":"/run/lantern-config","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config","Type":"bind"},{"Destination":"/etc/ntp.conf","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/private/fake-config/ntp.conf","Type":"bind"},{"Destination":"/state","Driver":"local","Mode":"z","Name":"fixture_state-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_state-0/_data","Type":"volume"},{"Destination":"/data","Driver":"local","Mode":"z","Name":"fixture_backup-0","Propagation":"","RW":true,"Source":"/var/lib/docker/volumes/fixture_backup-0/_data","Type":"volume"},{"Destination":"/run/lantern-bench-heap","Mode":"","Propagation":"rprivate","RW":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"name":"/fixture-case-lantern-0-1","oneoff":"False","owner":"","project":"fixture-case","restart_count":0,"schema":1,"service":"lantern-0","state":{"exit_code":0,"oom_killed":false,"paused":false,"pid":101,"restarting":false,"running":true,"started_at":"2026-10-30T00:00:01Z","status":"running"},"user":"501:20","working_dir":"/app"}\n', 'post.before.json': '{"daemon_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.before.daemon","sha256":"8481f32925a457ca44a519ae469096ac725cae11dc553d643ab456d1e3601547"},"host_identity":{"device":"1","gid":20,"inode":"100","mode":448,"type":"directory","uid":501},"inspect_file":{"path":"/synthetic/heap-bind-fixture/heap-mount-evidence/lantern-0/post.before.inspect.json","sha256":"4ff7732eb5cacb6a5114a6891161fa9497f1a1eb1ad15408934a2f7f66a2d226"},"phase":"post","position":"before","protocol":"symmetric_post_gc_2_v1","schema":1,"service":"lantern-0"}\n', 'probe-completed.json': '{"Config":{"Cmd":["stat","-c","%d:%i:%F","/proof/original-0","/proof/clone-0"],"Entrypoint":["/bin/busybox"],"Env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","LANTERN_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],"Image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","Labels":{"com.docker.compose.oneoff":"True","com.docker.compose.project":"fixture-case","com.docker.compose.service":"lantern-0-bind-proof","lantern.initializer":"lantern-owned-init.ABC123","lantern.owner":"compose-bind-identity-probe"},"User":"501:20"},"HostConfig":{"AutoRemove":false,"Binds":null,"CapAdd":null,"CapDrop":["ALL"],"DeviceCgroupRules":null,"DeviceRequests":null,"Devices":null,"Memory":33554432,"Mounts":[{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":true,"Source":"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/proof/original-0","Type":"bind"},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/proof/clone-0","Type":"bind"}],"NanoCpus":250000000,"NetworkMode":"none","PidMode":"","PidsLimit":16,"PortBindings":null,"Privileged":false,"PublishAllPorts":false,"ReadonlyRootfs":true,"RestartPolicy":{"MaximumRetryCount":0,"Name":"no"},"SecurityOpt":["no-new-privileges:true"],"Tmpfs":null,"UTSMode":"","UsernsMode":"","VolumesFrom":null},"Id":"3333333333333333333333333333333333333333333333333333333333333333","Image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","Mounts":[{"Destination":"/proof/original-0","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"},{"Destination":"/proof/clone-0","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"Name":"/fixture-case-lantern-0-bind-proof","RestartCount":0,"State":{"ExitCode":0,"OOMKilled":false,"Running":false,"StartedAt":"2026-10-30T00:00:00Z","Status":"exited"}}\n', 'probe-created.json': '{"Config":{"Cmd":["stat","-c","%d:%i:%F","/proof/original-0","/proof/clone-0"],"Entrypoint":["/bin/busybox"],"Env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","LANTERN_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],"Image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","Labels":{"com.docker.compose.oneoff":"True","com.docker.compose.project":"fixture-case","com.docker.compose.service":"lantern-0-bind-proof","lantern.initializer":"lantern-owned-init.ABC123","lantern.owner":"compose-bind-identity-probe"},"User":"501:20"},"HostConfig":{"AutoRemove":false,"Binds":null,"CapAdd":null,"CapDrop":["ALL"],"DeviceCgroupRules":null,"DeviceRequests":null,"Devices":null,"Memory":33554432,"Mounts":[{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":true,"Source":"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/proof/original-0","Type":"bind"},{"BindOptions":{"Propagation":"rprivate"},"ReadOnly":true,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Target":"/proof/clone-0","Type":"bind"}],"NanoCpus":250000000,"NetworkMode":"none","PidMode":"","PidsLimit":16,"PortBindings":null,"Privileged":false,"PublishAllPorts":false,"ReadonlyRootfs":true,"RestartPolicy":{"MaximumRetryCount":0,"Name":"no"},"SecurityOpt":["no-new-privileges:true"],"Tmpfs":null,"UTSMode":"","UsernsMode":"","VolumesFrom":null},"Id":"3333333333333333333333333333333333333333333333333333333333333333","Image":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","Mounts":[{"Destination":"/proof/original-0","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"},{"Destination":"/proof/clone-0","Mode":"ro","Propagation":"rprivate","RW":false,"Source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","Type":"bind"}],"Name":"/fixture-case-lantern-0-bind-proof","RestartCount":0,"State":{"ExitCode":0,"OOMKilled":false,"Running":false,"StartedAt":"0001-01-01T00:00:00Z","Status":"created"}}\n', 'probe-output.log': '9:123:directory\n9:123:directory\n', 'probe-remove.log': '3333333333333333333333333333333333333333333333333333333333333333\n', 'probe-wait.log': '0\n', 'requested-pairs.json': '[{"clone_source":"/host_mnt/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","declared_source":"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0","destination":"/run/lantern-bench-heap","expected_type":"directory","original_source":"/synthetic/heap-bind-fixture/heap-control-evidence/lantern-0"}]\n'}


def mount_fixture(root, index, phase, context, session):
    service = f"lantern-{index}"
    old_root = "/synthetic/heap-bind-fixture"
    base = root / "heap-mount-evidence" / service
    base.mkdir(parents=True, exist_ok=True, mode=0o700)
    base.chmod(0o700)
    files = {}
    for name, data in _MOUNT_FIXTURE_FILES.items():
        for old, token in (("1" * 64, "ORIGINALCID"), ("2" * 64, "CLONECID"), ("3" * 64, "PROBECID")):
            data = data.replace(old, token)
        data = data.replace(old_root, str(root)).replace("lantern-0", service).replace("fixture-case", context["run_id"])
        data = data.replace("ORIGINALCID", session["container_id"]).replace("CLONECID", f"{index + 101:064x}").replace("PROBECID", f"{index + 201:064x}")
        data = data.replace("sha256:" + "c" * 64, context["image_id"])
        # Docker wait/daemon/stat/remove outputs are raw bytes even when
        # their text happens to be a valid JSON scalar (notably "0\n").
        files[name] = json.loads(data) if name.endswith(".json") else data
    for name, value in files.items():
        if name in ("host-precreate.json", "host-before.json", "host-after.json"):
            value["inode"] = str(100 + index)
        elif isinstance(value, dict) and "host_identity" in value:
            value["host_identity"]["inode"] = str(100 + index)
    files["binding.json"]["context"] = {k: v for k, v in context.items() if k != "workload"}
    files["binding.json"]["session"] = session
    encoded = {}
    def encode(name):
        if name in encoded:
            return encoded[name]
        value = files[name]
        def refresh(v):
            if isinstance(v, dict):
                if set(v) == {"path", "sha256"}:
                    target = Path(v["path"]).name
                    v["sha256"] = hashlib.sha256(encode(target)).hexdigest()
                else:
                    for child in v.values(): refresh(child)
            elif isinstance(v, list):
                for child in v: refresh(child)
        refresh(value)
        encoded[name] = (value.encode() if isinstance(value, str) else json.dumps(value, sort_keys=True, separators=(",", ":")).encode())
        return encoded[name]
    for name in files:
        path = base / name
        path.write_bytes(encode(name)); path.chmod(0o600)
    return {"mount_binding_file": {"path": str(base / "binding.json"), "sha256": hashlib.sha256(encoded["binding.json"]).hexdigest()},
            **{f"mount_inspect_{position}_file": {"path": str(base / f"{phase}.{position}.json"),
                "sha256": hashlib.sha256(encoded[f"{phase}.{position}.json"]).hexdigest()}
               for position in ("before", "after")}}


class PairTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.normal = Path(self.temp.name).resolve() / "normal"
        self.tagged = Path(self.temp.name).resolve() / "tagged"
        self.expected = {"source_commit": "a" * 40, "source_tree": "b" * 40,
                         "normal_image_id": "sha256:" + "c" * 64,
                         "retained_image_id": "sha256:" + "d" * 64,
                         "runtime_user": "501:20", "cpuset": "0-3"}
        self.scenario = {"name": "receipt", "steady": {"duration": "45s"}}
        for root, image in ((self.normal, self.expected["normal_image_id"]),
                            (self.tagged, self.expected["retained_image_id"])):
            write(root / "scenario.resolved.json", self.scenario)
            for name in ("perf_gate", "workload_gate"):
                write(root / (name + ".json"), {"verdict": "pass"})
            provenance = {"source_commit": self.expected["source_commit"], "image_id": image,
                          "runtime_user": "501:20", "replicas": [
                              {"service": f"lantern-{i}", "container_id": str(i + 1) * 64,
                               "image_id": image, "runtime_user": "501:20", "cpuset": "0-3"}
                              for i in range(3)]}
            for phase in ("pre", "post"):
                write(root / f"image_provenance_{phase}.json", provenance)
        write(self.normal / "leak_gate.json", {"snapshot_protocol": pair.NORMAL_PROTOCOL,
              "verdict": "pass", "natural_checks_verdict": "pass",
              "natural_post_heap_observation_verdict": "fail", "retained_verdict": "requires_pair",
              "pair_required": True, "overall_memory_verdict": "requires_pair",
              "thresholds": {"goroutine_max_delta": 15, "heap_alloc_max_delta_mb": 32,
                             "steady_heap_alloc_max_delta_mb": 40}})
        snapshots = [{"endpoint": endpoint, "heap_alloc_bytes": 1000, "goroutines": 10}
                     for endpoint in pair.ENDPOINTS]
        write(self.normal / "runtime_pre.json", snapshots)
        post = copy.deepcopy(snapshots)
        post[0]["heap_alloc_bytes"] += 33 * pair.MIB
        write(self.normal / "runtime_post.json", post)
        write(self.normal / "runtime_steady.json", {"interval_ms": 5000, "duration_ms": 45000,
              "endpoints": list(pair.ENDPOINTS), "samples": [{"elapsed_ms": at, "replicas": snapshots}
                                                              for at in range(0, 45001, 5000)]})
        for phase, count in (("warmup", 10), ("steady", 20)):
            for i, name in enumerate(("receipt_admission", "receipt_lookup")):
                write(self.tagged / f"ghz_{phase}_{i}_{name}.json",
                      {"count": count, "statusCodeDistribution": {"OK": count}})
        self.cuts = {}
        for phase in ("baseline", "post"):
            count, forced, cycle = (10, 0, 5) if phase == "baseline" else (30, 2, 10)
            start = 1_000_000 if phase == "baseline" else 2_000_000_000
            cut = {"schema": 1, "protocol": pair.PROTOCOL, "phase": phase, "status": "complete",
                   "controller_monotonic_start_ns": start,
                   "controller_monotonic_end_ns": start + 400_000_000,
                   "context": {"run_id": "retained-test-run", "source_commit": "a" * 40,
                       "source_tree": "b" * 40, "image_id": self.expected["retained_image_id"],
                       "scenario_sha256": pair.canonical_sha(self.scenario), "runtime_user": "501:20",
                       "cpuset": "0-3", "workload": {"admission": count, "lookup": count}}, "replicas": []}
            for index, endpoint in enumerate(pair.ENDPOINTS):
                session = {"schema": 1, "protocol": pair.PROTOCOL, "run_nonce": "e" * 64,
                           "source_commit": "a" * 40, "source_tree": "b" * 40,
                           "image_id": self.expected["retained_image_id"], "container_id": str(index + 1) * 64,
                           "binary_sha256": "f" * 64, "uid": 501}
                identity = {"session": session, "pid": 1, "start_nonce": str(index + 4) * 64}
                request = {"schema": 1, "protocol": pair.PROTOCOL, "identity": identity,
                           "phase": phase, "ordinal": 1 if phase == "baseline" else 2,
                           "expected_receipts": count,
                           "cut_nonce": f"{index + (1 if phase == 'baseline' else 4):064x}"}
                heap = 1000 + (32 * pair.MIB if phase == "post" else 0)
                collections = [{"started_ns": 250_000_000 + i * 2_000_000,
                    "returned_ns": 251_000_000 + i * 2_000_000,
                    "before": memory(cycle + i, forced + i, heap),
                    "after": memory(cycle + i + 1, forced + i + 1, heap)} for i in range(2)]
                completion = {"schema": 1, "protocol": pair.PROTOCOL, "request": request,
                              "status": "complete", "reason": "", "elapsed_ns": 300_000_000,
                              "before": native(count), "after": native(count),
                              "collections": collections, "memory": collections[1]["after"]}
                cut["replicas"].append({"endpoint": endpoint, "completion": completion})
            self.cuts[phase] = cut
        self.save()

    def save(self):
        refs = {}
        for phase, suffix in (("baseline", "pre"), ("post", "post")):
            cut = self.cuts[phase]
            for index, row in enumerate(cut["replicas"]):
                completion = row["completion"]
                request = completion["request"]
                directory = self.tagged / "heap-control-evidence" / f"lantern-{index}"
                row.update(mount_fixture(self.tagged, index, phase, cut["context"], request["identity"]["session"]))
                row["session_file"] = write(directory / "session.json", request["identity"]["session"])
                row["ready_file"] = write(directory / "ready.json",
                    {"schema": 1, "protocol": pair.PROTOCOL, "identity": request["identity"]})
                row["request_file"] = write(directory / f"{phase}.request.json", request)
                row["started_file"] = write(directory / f"{phase}.started.json", {"request": request})
                row["completion_file"] = write(directory / f"{phase}.complete.json", completion)
            refs[suffix] = write(self.tagged / f"retained_heap_{suffix}.json", cut)
        write(self.tagged / "leak_gate.json", {"snapshot_protocol": pair.PROTOCOL, "verdict": "pass",
              "retained_verdict": "pass", "pair_required": True,
              "overall_memory_verdict": "requires_pair", "retained_inputs": refs})

    def validate(self):
        return pair.validate_pair(self.normal, self.tagged, self.scenario, self.expected)

    def test_same_source_pair_accepts_boundary_and_keeps_natural_failure_observation(self):
        result = self.validate()
        self.assertEqual(result["verdict"], "pass")
        self.assertEqual([r["heap_alloc_delta_bytes"] for r in result["replicas"]], [32 * pair.MIB] * 3)
        self.assertNotEqual(result["normal_image_id"], result["retained_image_id"])

    def test_forged_pass_cannot_hide_retained_excess(self):
        row = self.cuts["post"]["replicas"][0]["completion"]
        row["memory"]["heap_alloc"] += 1
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "32 MiB"):
            self.validate()

    def test_incomplete_asymmetric_active_and_cycle_drift_refuse(self):
        cases = (
            lambda row: row.update(status="incomplete"),
            lambda row: row.update(collections=row["collections"][:1]),
            lambda row: row["before"]["public"].update(begun=31, active=1),
            lambda row: row["memory"].update(cycle_after=999),
            lambda row: row.update(elapsed_ns=25_000_000_001),
            lambda row: row["collections"][0]["after"].update(num_forced_gc=100),
        )
        original = copy.deepcopy(self.cuts)
        for change in cases:
            with self.subTest(change=change):
                self.cuts = copy.deepcopy(original)
                change(self.cuts["post"]["replicas"][0]["completion"])
                self.save()
                with self.assertRaises(pair.Incomplete):
                    self.validate()

    def test_frontier_mismatch_and_submitted_work_gap_refuse(self):
        row = self.cuts["post"]["replicas"][0]["completion"]
        for side in ("before", "after"):
            row[side]["origins"][0]["sequence"] -= 1
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "frontiers"):
            self.validate()
        self.cuts["post"]["replicas"][1]["completion"] = copy.deepcopy(row)
        self.save()
        with self.assertRaises(pair.Incomplete):
            self.validate()

    def test_replay_restart_and_foreign_source_refuse(self):
        original = copy.deepcopy(self.cuts)
        for field, value in (("start_nonce", "9" * 64), ("pid", 2)):
            with self.subTest(field=field):
                self.cuts = copy.deepcopy(original)
                self.cuts["post"]["replicas"][0]["completion"]["request"]["identity"][field] = value
                self.save()
                with self.assertRaises(pair.Incomplete):
                    self.validate()
        self.cuts = copy.deepcopy(original)
        row = self.cuts["post"]["replicas"][0]["completion"]["request"]
        row["cut_nonce"] = self.cuts["baseline"]["replicas"][0]["completion"]["request"]["cut_nonce"]
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "replayed"):
            self.validate()

    def test_missing_pair_hash_mismatch_and_legacy_protocol_refuse(self):
        self.expected["retained_image_id"] = self.expected["normal_image_id"]
        with self.assertRaises(pair.Incomplete):
            self.validate()
        self.expected["retained_image_id"] = "sha256:" + "d" * 64
        path = self.tagged / "heap-control-evidence/lantern-0/post.complete.json"
        path.write_text(path.read_text() + " ")
        with self.assertRaisesRegex(pair.Incomplete, "hash"):
            self.validate()
        self.save()
        value = json.loads((self.normal / "leak_gate.json").read_text())
        value["snapshot_protocol"] = "natural_gc_single"
        write(self.normal / "leak_gate.json", value)
        with self.assertRaises(pair.Incomplete):
            self.validate()

    def test_natural_peak_and_goroutine_cannot_hide_behind_pair_pass(self):
        path = self.normal / "runtime_steady.json"
        original = json.loads(path.read_text())
        for key, value in (("heap_alloc_bytes", 1001 + 40 * pair.MIB), ("goroutines", 26)):
            with self.subTest(key=key):
                bad = copy.deepcopy(original)
                bad["samples"][2]["replicas"][1][key] = value
                write(path, bad)
                with self.assertRaises(pair.Incomplete):
                    self.validate()

    def test_symlink_escape_duplicate_keys_and_nonfinite_json_refuse(self):
        root = self.tagged / "unsafe"
        root.symlink_to(self.normal, target_is_directory=True)
        with self.assertRaises(pair.Incomplete):
            pair.read(self.tagged, root / "perf_gate.json")
        for content in ('{"x":1,"x":2}', '{"x":NaN}'):
            path = self.tagged / "bad.json"
            path.write_text(content)
            with self.assertRaises(pair.Incomplete):
                pair.read(self.tagged, path)

    def test_refusal_regular_or_broken_symlink_overrides_completion(self):
        path = self.tagged / "heap-control-evidence/lantern-0/refusal.json"
        for linked in (False, True):
            with self.subTest(linked=linked):
                if linked:
                    path.symlink_to("missing-refusal-target")
                else:
                    path.write_text("{}")
                try:
                    with self.assertRaisesRegex(pair.Incomplete, "refusal"):
                        self.validate()
                finally:
                    path.unlink()

    def test_missing_native_fields_or_excessive_expected_receipts_refuse(self):
        original = copy.deepcopy(self.cuts)
        for field in ("vertices", "edges", "log_sequence", "receipt_bytes"):
            with self.subTest(field=field):
                self.cuts = copy.deepcopy(original)
                row = self.cuts["post"]["replicas"][0]["completion"]
                row["before"].pop(field)
                row["after"].pop(field)
                self.save()
                with self.assertRaises(pair.Incomplete):
                    self.validate()
        self.cuts = copy.deepcopy(original)
        row = self.cuts["post"]["replicas"][0]["completion"]
        row["request"]["expected_receipts"] = 8001
        self.cuts["post"]["context"]["workload"] = {"admission": 8001, "lookup": 8001}
        self.save()
        with self.assertRaises(pair.Incomplete):
            self.validate()

    def test_gc_counters_cannot_roll_back_with_individually_coherent_samples(self):
        row = self.cuts["post"]["replicas"][0]["completion"]
        row["collections"][1]["before"] = memory(1, 3)
        row["collections"][1]["after"] = memory(2, 4)
        row["memory"] = row["collections"][1]["after"]
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "rolled back"):
            self.validate()

    def test_case_root_ancestor_symlink_refuses_even_matching_bytes(self):
        alias = self.normal.parent / "ancestor-alias"
        alias.symlink_to(self.normal.parent, target_is_directory=True)
        with self.assertRaises(pair.Incomplete):
            pair.validate_pair(alias / "normal", self.tagged, self.scenario, self.expected)

    def refresh_mount_refs(self):
        """Rehash changed synthetic evidence so semantic negatives cannot pass by hash-only rejection."""
        for index in range(3):
            base = self.tagged / "heap-mount-evidence" / f"lantern-{index}"
            files = {}
            for path in base.iterdir():
                if path.is_symlink() or not path.is_file():
                    continue
                files[path.name] = (json.loads(path.read_bytes()) if path.name.endswith(".json")
                                    else path.read_bytes())
            encoded = {}
            def encode(name):
                if name in encoded: return encoded[name]
                value = files[name]
                def refresh(v):
                    if isinstance(v, dict):
                        if set(v) == {"path", "sha256"}:
                            v["sha256"] = hashlib.sha256(encode(Path(v["path"]).name)).hexdigest()
                        else:
                            for child in v.values(): refresh(child)
                    elif isinstance(v, list):
                        for child in v: refresh(child)
                refresh(value)
                encoded[name] = value if isinstance(value, bytes) else json.dumps(value, sort_keys=True).encode()
                return encoded[name]
            for name in files:
                path = base / name
                path.write_bytes(encode(name)); path.chmod(0o600)
            for phase in ("baseline", "post"):
                row = self.cuts[phase]["replicas"][index]
                for key, name in (("mount_binding_file", "binding.json"), ("mount_inspect_before_file", phase + ".before.json"), ("mount_inspect_after_file", phase + ".after.json")):
                    row[key] = {"path": str(base / name), "sha256": hashlib.sha256(encoded[name]).hexdigest()}
        refs = {suffix: write(self.tagged / f"retained_heap_{suffix}.json", self.cuts[phase])
                for phase, suffix in (("baseline", "pre"), ("post", "post"))}
        report = json.loads((self.tagged / "leak_gate.json").read_bytes())
        report["retained_inputs"] = refs
        write(self.tagged / "leak_gate.json", report)

    def test_mount_literal_and_frozen_clone_representation_accept(self):
        base = self.tagged / "heap-mount-evidence/lantern-0"
        canonical = str(self.tagged / "heap-control-evidence/lantern-0")
        for name in ("clone-before.json", "clone-after.json"):
            value = json.loads((base / name).read_bytes())
            value["host_config"]["mounts"][-1]["Source"] = canonical
            value["mounts"][-1]["Source"] = canonical
            write(base / name, value)
        self.refresh_mount_refs()
        self.assertEqual(self.validate()["verdict"], "pass")
        for index in range(3):
            base = self.tagged / "heap-mount-evidence" / f"lantern-{index}"
            canonical = str(self.tagged / "heap-control-evidence" / f"lantern-{index}")
            for name in ("original-before.json", "original-after.json", "clone-before.json", "clone-after.json", "baseline.before.inspect.json", "baseline.after.inspect.json", "post.before.inspect.json", "post.after.inspect.json"):
                value = json.loads((base / name).read_bytes())
                value["host_config"]["mounts"][-1]["Source"] = canonical
                value["mounts"][-1]["Source"] = canonical
                write(base / name, value)
            binding = json.loads((base / "binding.json").read_bytes()); binding["probe"] = None
            write(base / "binding.json", binding); write(base / "requested-pairs.json", [])
        self.refresh_mount_refs()
        self.assertEqual(self.validate()["verdict"], "pass")

    def test_mount_semantic_forgery_refuses_after_all_hashes_rebound(self):
        changes = (
            ("host-after.json", lambda v: v.update(inode="999")),
            ("binding.json", lambda v: v.update(daemon_id="foreign-daemon")),
            ("binding.json", lambda v: v["session"].update(container_id="9" * 64)),
            ("binding.json", lambda v: v.update(allowedSources=["/unproved"])),
            ("binding.json", lambda v: v.update(probe=None)),
            ("clone-before.json", lambda v: v["mounts"][-1].update(Source="/unproved")),
            ("clone-before.json", lambda v: v["host_config"]["mounts"][-1]["BindOptions"].update(Propagation="shared")),
            ("original-before.json", lambda v: v["host_config"]["mounts"][0]["VolumeOptions"].update(NoCopy=False)),
            ("original-before.json", lambda v: v.update(Env=["SECRET=must-not-be-here"])),
            ("post.before.inspect.json", lambda v: v["state"].update(pid=9999)),
            ("probe-created.json", lambda v: v["HostConfig"].update(NetworkMode="host")),
            ("probe-created.json", lambda v: v["Mounts"][0].update(Source="/third-path")),
            ("probe-created.json", lambda v: v["Config"].update(Env=["TOKEN=secret"])),
        )
        for filename, change in changes:
            with self.subTest(filename=filename):
                self.save()
                base = self.tagged / "heap-mount-evidence/lantern-0"
                value = json.loads((base / filename).read_bytes()); change(value)
                write(base / filename, value)
                self.refresh_mount_refs()
                with self.assertRaises(pair.Incomplete): self.validate()

    def test_mount_missing_external_and_replaced_raw_proof_refuse(self):
        for kind in ("missing", "symlink", "foreign-node", "changed-binding"):
            with self.subTest(kind=kind):
                self.save()
                row = self.cuts["post"]["replicas"][0]
                path = self.tagged / "heap-mount-evidence/lantern-0/probe-output.log"
                if kind == "missing": path.unlink()
                elif kind == "symlink":
                    path.unlink(); path.symlink_to("probe-wait.log")
                elif kind == "foreign-node":
                    row["mount_binding_file"] = self.cuts["post"]["replicas"][1]["mount_binding_file"]
                    write(self.tagged / "retained_heap_post.json", self.cuts["post"])
                else:
                    row["mount_binding_file"] = dict(row["mount_binding_file"], sha256="0" * 64)
                    write(self.tagged / "retained_heap_post.json", self.cuts["post"])
                with self.assertRaises(pair.Incomplete): self.validate()
                if path.is_symlink(): path.unlink()

    def test_mount_probe_default_oom_killer_equivalence_is_typed_and_field_scoped(self):
        cases = ((None, False, True), ("missing", False, True), (False, None, True),
                 (False, True, False), (True, False, False), (False, 0, False),
                 (0, 0, False), (True, True, True))
        for before, after, accepted in cases:
            with self.subTest(before=before, after=after):
                self.save()
                base = self.tagged / "heap-mount-evidence/lantern-0"
                for name, value in (("probe-created.json", before), ("probe-completed.json", after)):
                    path = base / name
                    row = json.loads(path.read_bytes())
                    if value == "missing":
                        row["HostConfig"].pop("OomKillDisable", None)
                    else:
                        row["HostConfig"]["OomKillDisable"] = value
                    write(path, row)
                self.refresh_mount_refs()
                if accepted:
                    self.assertEqual(self.validate()["verdict"], "pass")
                else:
                    with self.assertRaises(pair.Incomplete): self.validate()
