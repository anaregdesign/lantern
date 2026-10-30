import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import retained_heap_pair as pair
import bench_time_profile
from test_bench_time_profile import fixture as time_fixture


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


def policy_scenario(name="receipt_admission_lookup"):
    family, method = {
        "receipt_admission_lookup": ("receipt_edge_delete", "DeleteEdge"),
        "receipt_vertex_put_admission_lookup": ("receipt_vertex_put", "PutVertex"),
        "receipt_vertex_delete_admission_lookup": ("receipt_vertex_delete", "DeleteVertex"),
        "receipt_edge_add_admission_lookup": ("receipt_edge_add", "AddEdge"),
    }[name]
    return {"name": name, "description": "Synthetic independent reader fixture; not runtime evidence",
            "target": {"driver": family, "endpoints": ["localhost:6380", "localhost:6381", "localhost:6382"],
                       "calls": [{"name": "receipt_admission", "call": "graph.v1.LanternService/" + method, "rps": 100},
                                 {"name": "receipt_lookup", "call": "graph.v1.LanternService/GetReceiptStatus", "rps": 100}]},
            "cluster": {"receipt_wal": {"enabled": True, "retention": "1h", "max_entries": 8000, "max_bytes": 16777216}},
            "phases": {"warmup": {"duration": "10s", "rps": 200, "concurrency": 12},
                       "steady": {"duration": "45s", "rps": 200, "concurrency": 12}, "cooldown": "10s"},
            "perf_gate": {"min_steady_rps_total": 150, "max_p99_ms": 500, "max_non_ok_ratio": 0,
                          "producers": {"receipt_admission": {"min_steady_rps": 75, "max_p99_ms": 500, "max_non_ok_ratio": 0},
                                        "receipt_lookup": {"min_steady_rps": 75, "max_p99_ms": 200, "max_non_ok_ratio": 0}}},
            "leak_gate": {"goroutine_max_delta": 15, "heap_alloc_max_delta_mb": 32,
                          "steady_heap_alloc_max_delta_mb": 40, "steady_sample_interval": "5s"},
            "goroutine_policy": {"protocol": "receipt_goroutine_operational_v1", "sampled_absolute_max": 128,
                                 "post_max_delta": 15, "post_offsets_seconds": [5, 10], "capture_window_seconds": 3}}


def file_ref(path):
    return {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def refresh_policy(root):
    path = root / "goroutine_policy_input.json"
    value = json.loads(path.read_bytes())
    def refresh(obj):
        if isinstance(obj, dict):
            if set(obj) == {"path", "sha256"}:
                obj["sha256"] = file_ref(Path(obj["path"]))["sha256"]
            else:
                for child in obj.values(): refresh(child)
        elif isinstance(obj, list):
            for child in obj: refresh(child)
    refresh(value)
    ref = write(path, value)
    report = json.loads((root / "leak_gate.json").read_bytes())
    report["goroutine_policy_input"] = ref
    write(root / "leak_gate.json", report)
    return value


def policy_fixture(root, expected, scenario):
    # Every retained raw reference is written independently; assertions call the
    # consumer on these files rather than accepting a supplied sidecar verdict.
    scenario_ref = write(root / "scenario.resolved.json", scenario)
    observer_sha = "e" * 64
    buildinfo = root / "goroutine_policy_buildinfo.txt"
    buildinfo.write_text("synthetic prebuilt observer fixture, not a binary qualification\n"); buildinfo.chmod(0o644)
    version = root / "goroutine_policy_go_version.txt"
    version.write_text("go version go1.27.3 fixture/fixture\n"); version.chmod(0o644)
    write(root / "goroutine_policy_observer.json", {"source_commit": expected["source_commit"], "source_tree": expected["source_tree"],
        "observer_sha256": observer_sha, "observer_go_version": "go1.27.3", "buildinfo_sha256": file_ref(buildinfo)["sha256"]})
    profile = {"schema": 1, "protocol": pair.GOROUTINE_PROTOCOL, "scenario": scenario["name"],
        "family": scenario["target"]["driver"], "image_scope": "normal", "measurement_protocol": "none",
        "source_commit": expected["source_commit"], "source_tree": expected["source_tree"], "image_id": expected["normal_image_id"],
        "config_sha256": "f" * 64, "scenario_sha256": scenario_ref["sha256"], "observer_sha256": observer_sha,
        "observer_go_version": "go1.27.3", "metrics_transport": "operator_docker_exec", "bridge_pid": 5011,
        "bridge_ports": [9390, 9391, 9392], "cpuset": "0-3", "runtime_user": expected["runtime_user"], "nodes": []}
    passive = []
    for i, endpoint in enumerate(pair.ENDPOINTS):
        peers = [f"https://lantern-{j}:6381" for j in range(3) if j != i]
        node = {"endpoint": endpoint, "container_id": str(i + 1) * 64, "pid": 1001+i,
                "started_at": "2026-10-30T00:00:00Z", "expected_peers": peers}
        profile["nodes"].append(node)
        passive.append({"subscribe_active": 2, "changes_active": 0, "anti_entropy_cycles": 1, "peers": {p: 1 for p in peers}})
    before = write(root / "goroutine_policy_before_post_profile.json", profile)
    baseline = copy.deepcopy(profile)
    for i, node in enumerate(baseline["nodes"]):
        path = root / f"goroutine_policy_pre_{9390+i}.prom"
        path.write_text("lantern_subscribe_active_streams 2\nlantern_changes_active_streams 0\nlantern_anti_entropy_cycles_total 1\n" +
                        "".join('lantern_peer_connected{peer="' + p + '"} 1\n' for p in node["expected_peers"]))
        path.chmod(0o600)
        node["passive_metrics"] = file_ref(path)
    baseline_ref = write(root / "goroutine_policy_baseline_profile.json", baseline)
    reports = []
    for i, phase in enumerate(("warmup", "steady")):
        count = 10 if phase == "warmup" else 20
        reports.append({"phase": phase,
            "admission": write(root / f"ghz_{phase}_0_receipt_admission.json", {"count": count, "statusCodeDistribution": {"OK": count}}),
            "lookup": write(root / f"ghz_{phase}_1_receipt_lookup.json", {"count": count, "statusCodeDistribution": {"OK": count}}),
            "coordinator_return": write(root / f"goroutine_policy_phase_{phase}_return.json", {
                "schema": 1, "protocol": pair.GOROUTINE_PROTOCOL, "phase": phase, "scope": "synchronous_receipt_phase_only",
                "started_ns": 1000+i*2000, "finished_ns": 2000+i*2000, "exit_code": 0, "coordinator_returned": True})})
    t0 = 10**12
    barrier = write(root / "goroutine_policy_barrier.json", {"schema": 1, "protocol": pair.GOROUTINE_PROTOCOL,
        "scope": "declared_client_work_only_not_global_idle", "t0_ns": t0, "semantic_complete": True, "pending_rpc": 0,
        "owned_work": [{"kind": k, "declared": n, "reaped": n, "pending_rpc": 0, "exit_failures": 0}
                       for k,n in (("producer", 2), ("subscriber", 0), ("chaos", 0))], "work_reports": reports})
    posts = []
    for offset in (5, 10):
        timing = {"offset_seconds": offset, "start_ns": t0+offset*10**9, "end_ns": t0+offset*10**9+200000000}
        write(root / f"goroutine_policy_timing_{offset}.json", timing)
        write(root / f"goroutine_policy_control_{offset}.json", {"schema": 1, "protocol": pair.GOROUTINE_PROTOCOL,
            "offset_seconds": offset, "metrics_urls": ["http://"+x+"/metrics" for x in pair.ENDPOINTS],
            "operator_loopback": True, "profile": before})
        capture = write(root / f"goroutine_policy_post_{offset}.json", {"schema": 1, "protocol": pair.GOROUTINE_PROTOCOL,
            "policy_role": "operative_normal_component", "qualification": False, "release_admission": False,
            "offset_seconds": offset, "profile": before, "started_utc": "2026-10-30T00:00:00Z", "duration_ns": 100000000,
            "replicas": [{"runtime": {"endpoint": endpoint, "goroutines": 10, "heap_alloc_bytes": 1000}, "passive": passive[i]}
                         for i, endpoint in enumerate(pair.ENDPOINTS)], "verdict": "captured"})
        posts.append({**timing, "capture": capture, "profile": write(root / f"goroutine_policy_post_{offset}_profile.json", profile)})
    source_refs = []
    for suffix in ("server/internal/security/control_renewal_network_test.go", "server/service/replication_delivery_test.go", "server/replication/pump_test.go"):
        path = root.parent / "source" / suffix
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("// synthetic source-reference fixture; not a runtime join proof\n"); path.chmod(0o644)
        source_refs.append(file_ref(path))
    workload = write(root / "workload_gate.json", {"verdict": "pass", "producer_failed": 0,
        "producer_diagnostics_failed": 0, "failed_consumers": 0, "chaos_failed": 0})
    input_ref = write(root / "goroutine_policy_input.json", {"schema": 1, "protocol": pair.GOROUTINE_PROTOCOL,
        "pre": file_ref(root / "runtime_pre.json"), "steady": file_ref(root / "runtime_steady.json"),
        "legacy_post": file_ref(root / "runtime_post.json"), "baseline_profile": baseline_ref,
        "before_post_profile": before, "barrier": barrier, "posts": posts, "workload_report": workload, "owner_evidence": source_refs})
    report = json.loads((root / "leak_gate.json").read_bytes())
    report.pop("natural_checks_verdict", None)
    report.update(snapshot_protocol=pair.OPERATIVE_NORMAL_PROTOCOL, natural_steady_memory_verdict="pass",
                  goroutine_checks_verdict="pass", goroutine_sampled_peak_limit=128, goroutine_post_residual_limit=15,
                  goroutine_policy_input=input_ref)
    write(root / "leak_gate.json", report)
    return input_ref


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
        self.scenario = policy_scenario()
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
        policy_fixture(self.normal, self.expected, self.scenario)
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
        for key, value in (("heap_alloc_bytes", 1001 + 40 * pair.MIB), ("goroutines", 129)):
            with self.subTest(key=key):
                bad = copy.deepcopy(original)
                bad["samples"][2]["replicas"][1][key] = value
                write(path, bad)
                refresh_policy(self.normal)
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

    def policy_report(self):
        return json.loads((self.normal / "leak_gate.json").read_bytes())

    def validate_policy(self):
        return pair.validate_normal_policy(self.normal, self.scenario, self.expected, self.policy_report())

    def change_policy_file(self, name, change):
        path = self.normal / name
        value = json.loads(path.read_bytes())
        change(value)
        write(path, value)
        refresh_policy(self.normal)

    def test_operative_four_family_profiles_and_legacy_interpretation_are_distinct(self):
        for name in ("receipt_admission_lookup", "receipt_vertex_put_admission_lookup",
                     "receipt_vertex_delete_admission_lookup", "receipt_edge_add_admission_lookup"):
            with self.subTest(name=name):
                self.scenario = policy_scenario(name)
                policy_fixture(self.normal, self.expected, self.scenario)
                self.change_policy_file("runtime_steady.json", lambda v: v["samples"][2]["replicas"][1].update(goroutines=26))
                self.assertEqual(self.validate_policy()["verdict"], "pass")
                old = self.policy_report()
                old["snapshot_protocol"] = pair.NORMAL_PROTOCOL
                with self.assertRaisesRegex(pair.Incomplete, "goroutine growth exceeds 15"):
                    pair.normal_observations(self.normal, old)

    def test_operative_absolute_boundary_and_recovered_peak_are_recomputed(self):
        self.change_policy_file("runtime_steady.json", lambda v: v["samples"][2]["replicas"][1].update(goroutines=128))
        self.assertEqual(self.validate_policy()["sampled_peaks"][1], 128)
        # A forged sidecar cannot hide a recovered active peak of 129.
        write(self.normal / "goroutine_policy.json", {"verdict": "pass"})
        self.change_policy_file("runtime_steady.json", lambda v: v["samples"][2]["replicas"][1].update(goroutines=129))
        with self.assertRaisesRegex(pair.Incomplete, "absolute 128"):
            self.validate_policy()

    def test_operative_original_and_both_fixed_residuals_block_even_after_recovery(self):
        original = (self.normal / "runtime_post.json").read_bytes()
        self.change_policy_file("runtime_post.json", lambda v: v[0].update(goroutines=26))
        with self.assertRaisesRegex(pair.Incomplete, "original POST"):
            self.validate_policy()
        (self.normal / "runtime_post.json").write_bytes(original)
        refresh_policy(self.normal)
        for offset in (5, 10):
            with self.subTest(offset=offset):
                path = self.normal / f"goroutine_policy_post_{offset}.json"
                raw = path.read_bytes()
                self.change_policy_file(path.name, lambda v: v["replicas"][0]["runtime"].update(goroutines=26))
                with self.assertRaisesRegex(pair.Incomplete, "fixed POST"):
                    self.validate_policy()
                path.write_bytes(raw)
                refresh_policy(self.normal)
        self.assertEqual(self.validate_policy()["verdict"], "pass")

    def test_operative_missing_late_duplicate_and_unjoined_work_refuse(self):
        cases = [
            ("goroutine_policy_input.json", lambda v: v["posts"].pop()),
            ("goroutine_policy_input.json", lambda v: v["posts"][0].update(end_ns=10**12+8*10**9+1)),
            ("goroutine_policy_input.json", lambda v: v["posts"][1].update(offset_seconds=5)),
            ("goroutine_policy_barrier.json", lambda v: v["owned_work"][0].update(reaped=1)),
            ("goroutine_policy_barrier.json", lambda v: v.update(pending_rpc=1)),
            ("goroutine_policy_barrier.json", lambda v: v["work_reports"][1].update(phase="warmup")),
            ("goroutine_policy_phase_steady_return.json", lambda v: v.update(exit_code=1)),
            ("goroutine_policy_phase_steady_return.json", lambda v: v.update(finished_ns=10**12+1)),
        ]
        for name, change in cases:
            with self.subTest(name=name, change=change):
                policy_fixture(self.normal, self.expected, self.scenario)
                self.change_policy_file(name, change)
                with self.assertRaises(pair.Incomplete): self.validate_policy()

    def test_operative_source_image_observer_process_passive_and_profile_mismatch_refuse(self):
        cases = [
            ("goroutine_policy_before_post_profile.json", lambda v: v.update(source_commit="d"*40)),
            ("goroutine_policy_before_post_profile.json", lambda v: v.update(image_id=self.expected["retained_image_id"])),
            ("goroutine_policy_before_post_profile.json", lambda v: v.update(config_sha256="a"*64)),
            ("goroutine_policy_before_post_profile.json", lambda v: v.update(image_scope="tagged")),
            ("goroutine_policy_before_post_profile.json", lambda v: v.update(family="receipt_vertex_put")),
            ("goroutine_policy_post_5_profile.json", lambda v: v["nodes"][1].update(pid=999)),
            ("goroutine_policy_post_5_profile.json", lambda v: v["nodes"][1].update(started_at="2026-10-30T00:00:01Z")),
            ("goroutine_policy_post_5_profile.json", lambda v: v.update(bridge_pid=999)),
            ("goroutine_policy_observer.json", lambda v: v.update(observer_sha256="a"*64)),
            ("goroutine_policy_post_5.json", lambda v: v["replicas"][0]["passive"].update(subscribe_active=3)),
            ("goroutine_policy_post_5.json", lambda v: v["replicas"][0]["passive"].update(anti_entropy_cycles=0)),
            ("goroutine_policy_post_5.json", lambda v: v.update(release_admission=True)),
        ]
        for name, change in cases:
            with self.subTest(name=name, change=change):
                policy_fixture(self.normal, self.expected, self.scenario)
                self.change_policy_file(name, change)
                with self.assertRaises(pair.Incomplete): self.validate_policy()

    def test_operative_reviewed_profile_cannot_be_rebound_to_other_work(self):
        cases = [lambda v: v["target"].update(driver="receipt_vertex_put"),
                 lambda v: v["target"].update(endpoints=["localhost:6380"]),
                 lambda v: v["phases"]["steady"].update(concurrency=13),
                 lambda v: v["phases"]["steady"].update(rps=201),
                 lambda v: v["cluster"]["receipt_wal"].update(max_entries=8001),
                 lambda v: v["perf_gate"].update(max_non_ok_ratio=False),
                 lambda v: v.update(metric_gate={}),
                 lambda v: v["goroutine_policy"].update(protocol="receipt_goroutine_candidate_v1")]
        for change in cases:
            with self.subTest(change=change):
                self.scenario = policy_scenario()
                change(self.scenario)
                policy_fixture(self.normal, self.expected, self.scenario)
                with self.assertRaises(pair.Incomplete): self.validate_policy()

    def test_operative_foreign_snapshot_profile_and_phase_return_refs_refuse(self):
        original = (self.normal / "goroutine_policy_input.json").read_bytes()
        foreign = self.normal.parent / "foreign"
        for key in ("pre", "baseline_profile", "barrier"):
            with self.subTest(key=key):
                (self.normal / "goroutine_policy_input.json").write_bytes(original)
                value = json.loads(original)
                ref = value[key]
                path = foreign / Path(ref["path"]).name
                path.parent.mkdir(exist_ok=True)
                path.write_bytes(Path(ref["path"]).read_bytes()); path.chmod(0o600)
                value[key] = file_ref(path)
                write(self.normal / "goroutine_policy_input.json", value)
                refresh_policy(self.normal)
                with self.assertRaisesRegex(pair.Incomplete, "physical current case"):
                    self.validate_policy()
        (self.normal / "goroutine_policy_input.json").write_bytes(original)
        policy_fixture(self.normal, self.expected, self.scenario)
        path = self.normal / "goroutine_policy_barrier.json"
        barrier = json.loads(path.read_bytes())
        ref = barrier["work_reports"][0]["coordinator_return"]
        dest = foreign / Path(ref["path"]).name
        dest.write_bytes(Path(ref["path"]).read_bytes()); dest.chmod(0o600)
        barrier["work_reports"][0]["coordinator_return"] = file_ref(dest)
        write(path, barrier); refresh_policy(self.normal)
        with self.assertRaisesRegex(pair.Incomplete, "physical current case"):
            self.validate_policy()

    def test_operative_private_modes_aliases_and_missing_original_post_refuse(self):
        path = self.normal / "goroutine_policy_input.json"
        raw = path.read_bytes()
        path.chmod(0o644)
        with self.assertRaisesRegex(pair.Incomplete, "mode"):
            self.validate_policy()
        path.chmod(0o600)
        value = json.loads(raw)
        value["Schema"] = 1
        write(path, value); refresh_policy(self.normal)
        with self.assertRaisesRegex(pair.Incomplete, "shape"):
            self.validate_policy()
        policy_fixture(self.normal, self.expected, self.scenario)
        (self.normal / "runtime_post.json").unlink()
        with self.assertRaises(pair.Incomplete): self.validate_policy()

    def test_fresh_pair_rejects_saved_old_normal_protocol_without_reinterpreting_it(self):
        value = self.policy_report()
        value["snapshot_protocol"] = pair.NORMAL_PROTOCOL
        write(self.normal / "leak_gate.json", value)
        with self.assertRaises(pair.Incomplete): self.validate()


    def test_operative_passive_counter_is_monotonic_across_both_fixed_posts(self):
        for offset in (5, 10):
            self.change_policy_file(f"goroutine_policy_post_{offset}.json",
                lambda value: value["replicas"][0]["passive"].update(anti_entropy_cycles=3))
        self.assertEqual(self.validate_policy()["verdict"], "pass")
        # PRE1 -> post5=3 -> post10=2 is a rollback even with stable PID/start.
        self.change_policy_file("goroutine_policy_post_10.json",
            lambda value: value["replicas"][0]["passive"].update(anti_entropy_cycles=2))
        with self.assertRaisesRegex(pair.Incomplete, "cycle counter rolled back"):
            self.validate_policy()

    def test_operative_nonroot_runtime_owner_is_portable_and_identity_bound(self):
        self.expected["runtime_user"] = "1000:1000"
        for suffix in ("pre", "post"):
            path = self.normal / f"image_provenance_{suffix}.json"
            value = json.loads(path.read_bytes())
            value["runtime_user"] = "1000:1000"
            for node in value["replicas"]:
                node["runtime_user"] = "1000:1000"
            write(path, value)
        policy_fixture(self.normal, self.expected, self.scenario)
        self.assertEqual(self.validate_policy()["verdict"], "pass")
        self.expected["runtime_user"] = "1001:1000"
        with self.assertRaises(pair.Incomplete):
            self.validate_policy()
        self.expected["runtime_user"] = "0:1000"
        policy_fixture(self.normal, self.expected, self.scenario)
        with self.assertRaises(pair.Incomplete):
            self.validate_policy()

    def test_local_time_pair_v3_recomputes_both_case_receipts(self):
        for root, image, scope in ((self.normal, self.expected["normal_image_id"], "normal"),
                                   (self.tagged, self.expected["retained_image_id"], "retained")):
            binding = time_fixture(root, dict(self.expected, image_id=image, image_scope=scope,
                                              run_id=root.name, scenario=self.scenario["name"]))
            self.expected.update({k: binding[k] for k in
                ("time_profile", "helper_sources", "go_tool_sha256", "goarch", "project")})
        with patch.object(pair, "bench_time_profile", bench_time_profile, create=True):
            proof = self.validate()
            self.assertEqual(proof["protocol"], "source_paired_retained_v3")
            self.assertFalse(proof["time_profile"]["normal"]["external_time_qualified"])
            normal = json.loads((self.normal / "time_source_pre.json").read_text())
            retained = json.loads((self.tagged / "time_source_pre.json").read_text())
            self.assertNotEqual(normal["relay_config"]["sha256"], retained["relay_config"]["sha256"])
            self.assertNotEqual(proof["time_profile"]["normal"]["frontend_id"], proof["time_profile"]["retained"]["frontend_id"])
            self.assertEqual(proof["time_profile"]["normal"]["pairing"], proof["time_profile"]["retained"]["pairing"])
            (self.tagged / "time_source_post.json").unlink()
            with self.assertRaisesRegex(pair.Incomplete, "paired time source incomplete"):
                self.validate()
