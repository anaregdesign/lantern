#!/usr/bin/env python3
"""Bounded private control for the measurement-tag-only heap observer.

No public endpoint or profiling escape. A signal is only a wakeup: only an
identity-bound target completion proves the two collections. The existing
owned Compose/host owner retains resource and process-group cleanup custody.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import selectors
import stat
import subprocess
import time
import sys

SCHEMA = 1
PROTOCOL = "symmetric_post_gc_2_v1"
TARGET_DIRECTORY = "/run/lantern-bench-heap"
INPUT_LIMIT = 4096
OUTPUT_LIMIT = 32768
CONTROLLER_NS = 30_000_000_000
BINARY_LIMIT = 128 << 20
CONFIG_FILE = "heap-control.json"
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
HEX40 = re.compile(r"[0-9a-f]{40}\Z")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def json_bytes(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode()


def _pairs(items):
    value = {}
    for key, child in items:
        if key in value:
            raise ValueError("duplicate JSON field")
        value[key] = child
    return value


def decode(data):
    return json.loads(data, object_pairs_hook=_pairs, parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite JSON")))


def private_directory(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise ValueError("control directory must be an absolute physical path")
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o700 or info.st_uid != os.getuid() or info.st_gid != os.getgid():
        raise ValueError("control directory ownership differs")
    return path


def read_private(path, limit, private=True):
    path = Path(path)
    if path.resolve() != path:
        raise ValueError("evidence path contains symlink")
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or (private and stat.S_IMODE(before.st_mode) != 0o600) or before.st_uid != os.getuid() or before.st_nlink != 1 or not 0 < before.st_size <= limit:
        raise ValueError("invalid bounded evidence file")
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        opened = os.fstat(fd)
        if not stat.S_ISREG(opened.st_mode) or (private and stat.S_IMODE(opened.st_mode) != 0o600) or opened.st_uid != os.getuid() or opened.st_nlink != 1 or not 0 < opened.st_size <= limit or (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
            raise ValueError("evidence file identity changed")
        with os.fdopen(fd, "rb", closefd=False) as source:
            data = source.read(limit + 1)
    finally:
        os.close(fd)
    after = path.lstat()
    fingerprint = lambda info: (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid, info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if len(data) != before.st_size or len(data) > limit or fingerprint(before) != fingerprint(opened) or fingerprint(before) != fingerprint(after):
        raise ValueError("evidence file changed")
    return data


def write_private(path, value, limit=OUTPUT_LIMIT):
    data = json_bytes(value)
    if len(data) > limit:
        raise ValueError("output exceeds bound")
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, "wb", closefd=False) as target:
            target.write(data)
            target.flush()
            os.fsync(fd)
    finally:
        os.close(fd)
    parent = os.open(Path(path).parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(parent)
    finally:
        os.close(parent)
    return {"path": str(path), "sha256": digest(data)}


def raw_reference(path, limit=OUTPUT_LIMIT):
    data = read_private(path, limit)
    return decode(data), {"path": str(path), "sha256": digest(data)}


def docker(argv, deadline):
    # Reserve one second INSIDE the controller budget for direct reap. The
    # enclosing original PG/Compose owner remains the cleanup authority.
    work_end = deadline - 1_000_000_000
    if time.monotonic_ns() >= work_end:
        raise TimeoutError("controller deadline before dispatch")
    child = subprocess.Popen(["docker", *argv], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    selector = selectors.DefaultSelector()
    streams = {"stdout": bytearray(), "stderr": bytearray()}
    for name, limit in (("stdout", 128 << 10), ("stderr", 32 << 10)):
        stream = getattr(child, name)
        os.set_blocking(stream.fileno(), False)
        selector.register(stream, selectors.EVENT_READ, (name, limit))
    try:
        while selector.get_map() or child.poll() is None:
            remaining = (work_end - time.monotonic_ns()) / 1e9
            if remaining <= 0:
                raise TimeoutError("uncertain Docker command")
            for key, _ in selector.select(min(remaining, 0.05)):
                name, limit = key.data
                data = os.read(key.fd, min(8192, limit - len(streams[name]) + 1))
                if not data:
                    selector.unregister(key.fileobj)
                else:
                    streams[name].extend(data)
                    if len(streams[name]) > limit:
                        raise ValueError("Docker output exceeded evidence bound")
        if child.wait(timeout=max(0.001, (deadline-time.monotonic_ns()) / 1e9)) != 0:
            raise ValueError("Docker command failed")
        return bytes(streams["stdout"])
    except (OSError, ValueError, TimeoutError):
        if child.poll() is None:
            try:
                child.kill()
            except ProcessLookupError:
                pass
        try:
            child.wait(timeout=max(0.001, (deadline-time.monotonic_ns()) / 1e9))
        except subprocess.TimeoutExpired:
            raise TimeoutError("Docker child custody unproven") from None
        raise
    finally:
        selector.close()
        child.stdout.close()
        child.stderr.close()


# Host-only original-first directory binding; never a Server control surface.
MOUNT_LIMIT = 128 << 10
MOUNT_TOTAL = 2 << 20
MOUNT_FILES = 32
TARGET = TARGET_DIRECTORY


def host_identity(path):
    directory = private_directory(path)
    info = directory.lstat()
    return {'device': str(info.st_dev), 'inode': str(info.st_ino), 'type': 'directory',
            'uid': info.st_uid, 'gid': info.st_gid, 'mode': stat.S_IMODE(info.st_mode)}


def mount_stage(root, service):
    if service not in ('lantern-0', 'lantern-1', 'lantern-2'):
        raise ValueError('fixed heap service required')
    return private_directory(Path(root).parent / 'heap-mount-staging' / service)


def watched(row):
    # Project only watched fields while full Docker Config/Env stays in memory.
    c, h, s = row['Config'], row['HostConfig'], row['State']
    required_host={'NetworkMode','Binds','Mounts','ReadonlyRootfs','Privileged','AutoRemove','CapAdd','CapDrop','SecurityOpt','PidMode','IpcMode','UTSMode','UsernsMode','RestartPolicy'}
    required_state={'Status','Running','Pid','StartedAt','ExitCode','OOMKilled','Restarting','Paused'}
    if not required_host<=set(h) or not required_state<=set(s) or not {'User','Labels','Entrypoint','Cmd','WorkingDir'}<=set(c) or 'RestartCount' not in row:
        raise ValueError('incomplete firsthand inspect')
    labels = c.get('Labels') or {}
    result = {'schema': 1, 'kind': 'heap-mount-observation', 'id': row['Id'], 'name': row['Name'], 'image': row['Image'],
            'user': c['User'], 'project': labels.get('com.docker.compose.project', ''),
            'service': labels.get('com.docker.compose.service', ''), 'oneoff': labels.get('com.docker.compose.oneoff', ''),
            'owner': labels.get('lantern.owner', ''), 'initializer': labels.get('lantern.initializer', ''),
            'entrypoint': c.get('Entrypoint'), 'cmd': c.get('Cmd'), 'working_dir': c.get('WorkingDir', ''),
            'host_config': {'network_mode': h.get('NetworkMode', ''), 'binds': h.get('Binds'), 'mounts': h.get('Mounts'),
                            'readonly_rootfs': h.get('ReadonlyRootfs', False), 'privileged': h.get('Privileged', False),
                            'auto_remove': h.get('AutoRemove', False), 'cap_add': h.get('CapAdd'), 'cap_drop': h.get('CapDrop'),
                            'security_opt': h.get('SecurityOpt'), 'pid_mode': h.get('PidMode', ''), 'ipc_mode': h.get('IpcMode', ''),
                            'uts_mode': h.get('UTSMode', ''), 'userns_mode': h.get('UsernsMode', ''),
                            'restart_policy': h.get('RestartPolicy'), 'tmpfs': h.get('Tmpfs')},
            'mounts': row['Mounts'], 'state': {'status': s['Status'], 'running': s['Running'], 'pid': s.get('Pid', 0),
                                           'started_at': s.get('StartedAt', ''), 'exit_code': s.get('ExitCode', 0),
                                           'oom_killed': s.get('OOMKilled', False), 'restarting': s.get('Restarting', False),
                                           'paused': s.get('Paused', False)}, 'restart_count': row.get('RestartCount', 0)}
    validate_observation(result)
    return result


def stable_observation(value):
    return {k: v for k, v in value.items() if k != 'state'}


def heap_rows(value):
    requests = [m for m in value['host_config']['mounts'] if m.get('Target') == TARGET]
    mounts = [m for m in value['mounts'] if m.get('Destination') == TARGET]
    if len(requests) != 1 or len(mounts) != 1 or len(value['mounts']) != 5 or len(value['host_config']['mounts']) != 3:
        raise ValueError('exact additional heap bind required')
    request, mount = requests[0], mounts[0]
    if set(request) not in ({'Type', 'Source', 'Target', 'BindOptions'}, {'Type', 'Source', 'Target', 'ReadOnly', 'BindOptions'}) or request['Target'] != TARGET or request.get('ReadOnly', False) is not False or request['BindOptions'] not in ({'Propagation': 'rprivate'}, {'Propagation': 'rprivate', 'CreateMountpoint': False}):
        raise ValueError('unsupported heap request')
    if set(mount) != {'Type', 'Source', 'Destination', 'Mode', 'RW', 'Propagation'} or mount['Type'] != 'bind' or mount['RW'] is not True or mount['Mode'] not in ('', 'rw') or mount['Propagation'] != 'rprivate':
        raise ValueError('unsupported effective heap mount')
    for source in (request['Source'], mount['Source']):
        if not isinstance(source, str) or not source.startswith('/') or len(source) <= 1 or any(ord(c) < 32 or ord(c) == 127 or c in ',"' for c in source):
            raise ValueError('unsafe heap source')
    if {m.get('Destination') for m in value['mounts']} != {TARGET,'/run/lantern-config','/etc/ntp.conf','/state','/data'}:
        raise ValueError('exact serving mount membership')
    for destination in ('/run/lantern-config','/etc/ntp.conf'):
        rows=[m for m in value['mounts'] if m.get('Destination')==destination]
        if len(rows)!=1 or rows[0].get('Type')!='bind' or rows[0].get('RW') is not False or rows[0].get('Mode') not in ('','ro') or rows[0].get('Propagation')!='rprivate':
            raise ValueError('protected config/time mount not read-only')
    for destination in ('/state','/data'):
        req=[m for m in value['host_config']['mounts'] if m.get('Target')==destination]
        eff=[m for m in value['mounts'] if m.get('Destination')==destination]
        if len(req)!=1 or len(eff)!=1 or req[0].get('Type')!='volume' or req[0].get('ReadOnly',False) is not False or req[0].get('VolumeOptions')!={'NoCopy':True} or not isinstance(req[0].get('Source'),str) or not req[0]['Source'] or eff[0].get('Type')!='volume' or eff[0].get('Name')!=req[0]['Source'] or eff[0].get('RW') is not True:
            raise ValueError('native state volume/NoCopy differs')
    return request, mount


def observed_sources(original, canonical):
    request, effective = heap_rows(original)
    extra = sorted({request['Source'], effective['Source']} - {canonical})
    if len(extra) > 2:
        raise ValueError('too many observed heap representations')
    return {canonical, *extra}, [{'destination': TARGET, 'declared_source': canonical, 'original_source': canonical,
                                  'clone_source': source, 'expected_type': 'directory'} for source in extra]


def checked_mount_ref(ref, case, binary=False, limit=MOUNT_LIMIT):
    if not isinstance(ref, dict) or set(ref) != {'path', 'sha256'} or not isinstance(ref['sha256'], str) or not HEX64.fullmatch(ref['sha256']):
        raise ValueError('invalid mount evidence reference')
    path = Path(ref['path'])
    if not path.is_absolute() or not path.is_relative_to(Path(case)) or '..' in path.parts:
        raise ValueError('mount reference escapes case')
    data = read_private(path, limit)
    if digest(data) != ref['sha256']:
        raise ValueError('mount reference changed or escapes case')
    return data if binary else decode(data)


def mount_directory_bounds(directory):
    directory = private_directory(directory)
    entries = list(directory.iterdir())
    if len(entries) > MOUNT_FILES or sum(p.lstat().st_size for p in entries) > MOUNT_TOTAL:
        raise ValueError('mount proof file/total bounds')
    for p in entries:
        read_private(p, MOUNT_LIMIT)


def validate_probe(probe, pairs, case, original):
    if not pairs:
        if probe is not None:
            raise ValueError('literal binding must not invent probe')
        return
    keys = {'created_file', 'completed_file', 'output_file', 'wait_file', 'remove_file', 'environment_file'}
    if not isinstance(probe, dict) or set(probe) != keys:
        raise ValueError('complete stat evidence required')
    a = checked_mount_ref(probe['created_file'], case)
    b = checked_mount_ref(probe['completed_file'], case)
    env = checked_mount_ref(probe['environment_file'], case)
    targets = [f'/proof/{side}-{i}' for i in range(len(pairs)) for side in ('original', 'clone')]
    for row in (a, b):
        if not isinstance(row.get('Id'), str) or not HEX64.fullmatch(row['Id']):
            raise ValueError('stat immutable ID')
        labels = row['Config'].get('Labels') or {}
        svc = original['service'] + '-bind-proof'
        if row.get('Name') != '/' + original['project'] + '-' + svc or labels.get('com.docker.compose.project') != original['project'] or labels.get('com.docker.compose.service') != svc or labels.get('com.docker.compose.oneoff') != 'True' or labels.get('lantern.owner') != 'compose-bind-identity-probe' or not re.fullmatch(r'lantern-owned-init\.[A-Za-z0-9]+', labels.get('lantern.initializer', '')):
            raise ValueError('stat exact ownership/nonce')
        c, h = row['Config'], row['HostConfig']
        if row['Image'] != original['image'] or c['Image'] != original['image'] or c['User'] != original['user'] or c['Entrypoint'] != ['/bin/busybox'] or c['Cmd'] != ['stat', '-c', '%d:%i:%F', *targets] or c['Env'] != env:
            raise ValueError('stat identity/command/environment')
        allowed = ('PATH', 'LANTERN_DEFAULT_TTL_SECONDS', 'LANTERN_PORT', 'LANTERN_METRICS_ADDR', 'LANTERN_LOG_FORMAT', 'LANTERN_LOG_LEVEL', 'LANTERN_VERSION', 'LANTERN_COMMIT')
        if not isinstance(env, list) or any(not isinstance(v, str) or '=' not in v or v.split('=', 1)[0] not in allowed or any(ch in v for ch in '\r\n\0') for v in env) or len({v.split('=', 1)[0] for v in env}) != len(env):
            raise ValueError('unsafe stat environment')
        fixed = {'NetworkMode': 'none', 'ReadonlyRootfs': True, 'Privileged': False, 'AutoRemove': False,
                 'CapDrop': ['ALL'], 'SecurityOpt': ['no-new-privileges:true'], 'Memory': 33554432,
                 'NanoCpus': 250000000, 'PidsLimit': 16, 'PidMode': '', 'UTSMode': '', 'UsernsMode': '', 'PublishAllPorts': False}
        if any(type(h.get(k)) is not type(v) or h.get(k) != v for k, v in fixed.items()) or h.get('CapAdd') not in (None, []) or h.get('RestartPolicy') != {'Name': 'no', 'MaximumRetryCount': 0}:
            raise ValueError('stat fixed isolation profile')
        if any(h.get(k) for k in ('Binds', 'VolumesFrom', 'Tmpfs', 'Devices', 'DeviceRequests', 'DeviceCgroupRules', 'PortBindings')) or c.get('Volumes'):
            raise ValueError('stat extra resources')
        if len(h['Mounts']) != len(targets) or len(row['Mounts']) != len(targets):
            raise ValueError('stat mount membership')
        for i, pair in enumerate(pairs):
            for side in ('original', 'clone'):
                target = f'/proof/{side}-{i}'
                req = [m for m in h['Mounts'] if m.get('Target') == target]
                eff = [m for m in row['Mounts'] if m.get('Destination') == target]
                if len(req) != 1 or len(eff) != 1 or set(req[0]) != {'Type','Source','Target','ReadOnly','BindOptions'} or set(eff[0]) != {'Type','Source','Destination','Mode','RW','Propagation'}:
                    raise ValueError('stat paired target')
                if req[0]['Source'] not in (pair['original_source'], pair['clone_source']) or eff[0]['Source'] not in (pair['original_source'], pair['clone_source']) or req[0]['ReadOnly'] is not True or eff[0]['RW'] is not False or req[0]['Type'] != 'bind' or eff[0]['Type'] != 'bind' or eff[0]['Mode'] not in ('', 'ro') or eff[0]['Propagation'] != 'rprivate' or req[0]['BindOptions'] not in ({'Propagation':'rprivate'}, {'Propagation':'rprivate','CreateMountpoint':False}):
                    raise ValueError('unproved stat source/profile')
    def probe_fingerprint(row):
        # Match the maintained initializer's default-enabled OOM representation.
        # Fold only copies; raw proof bytes and their references remain exact.
        result=decode(json_bytes({key:row[key] for key in ('Id','Name','Image','Config','HostConfig','Mounts')}))
        oom=result['HostConfig'].get('OomKillDisable')
        if oom is None:
            result['HostConfig']['OomKillDisable']=False
        elif type(oom) is not bool:
            raise ValueError('invalid stat OomKillDisable')
        return result
    if probe_fingerprint(a)!=probe_fingerprint(b):
        raise ValueError('stat fingerprint changed')
    if a['State'].get('StartedAt', '') not in ('', '0001-01-01T00:00:00Z') or a['State'].get('Pid', 0) != 0 or any(r['State'].get(k, False) is not False for r in (a,b) for k in ('OOMKilled','Paused','Restarting')) or a['State']['Status'] != 'created' or a['State']['Running'] is not False or b['State']['Status'] != 'exited' or b['State']['Running'] is not False or b['State']['ExitCode'] != 0 or b['State'].get('StartedAt','') in ('','0001-01-01T00:00:00Z') or b['State'].get('Pid',0)!=0 or b['State'].get('OOMKilled') or a.get('RestartCount', 0) != 0 or b.get('RestartCount', 0) != 0:
        raise ValueError('stat lifecycle incomplete')
    output = checked_mount_ref(probe['output_file'], case, True, 4096).decode()
    lines = output.splitlines()
    if output != '\n'.join(lines)+'\n' or len(lines) != 2 * len(pairs):
        raise ValueError('bounded stat output')
    for i in range(len(pairs)):
        if lines[2*i] != lines[2*i+1] or not re.fullmatch(r'[1-9][0-9]*:[1-9][0-9]*:directory', lines[2*i]):
            raise ValueError('different directory object')
    if checked_mount_ref(probe['wait_file'], case, True, 4096) != b'0\n' or checked_mount_ref(probe['remove_file'], case, True, 4096) != (a['Id']+'\n').encode():
        raise ValueError('stat wait/removal incomplete')


CHAIN_FILES = {
    'host_precreate_file': 'host-precreate.json', 'host_before_file': 'host-before.json', 'host_after_file': 'host-after.json',
    'original_before_file': 'original-before.json', 'original_after_file': 'original-after.json',
    'daemon_before_file': 'daemon-before', 'daemon_after_file': 'daemon-after', 'pairs_file': 'requested-pairs.json',
    'clone_before_file': 'clone-before.json', 'clone_after_file': 'clone-after.json',
}
PROBE_FILES = {'created_file': 'probe-created.json', 'completed_file': 'probe-completed.json', 'output_file': 'probe-output.log',
               'wait_file': 'probe-wait.log', 'remove_file': 'probe-remove.log', 'environment_file': 'image-environment.json'}


def chain_from_directory(directory, context, session):
    directory = Path(directory)
    ref = lambda name: {'path': str(directory/name), 'sha256': digest(read_private(directory/name, MOUNT_LIMIT))}
    refs = {key: ref(name) for key,name in CHAIN_FILES.items()}
    pairs = decode(read_private(directory/'requested-pairs.json', MOUNT_LIMIT))
    refs['probe'] = None if not pairs else {key: ref(name) for key,name in PROBE_FILES.items()}
    original = decode(read_private(directory/'original-before.json', MOUNT_LIMIT))
    refs.update(schema=1, protocol=PROTOCOL, context=context, service=original['service'], session=session,
                canonical_directory=str(directory.parent.parent/'heap-control-evidence'/original['service']),
                daemon_id=read_private(directory/'daemon-before',4096).decode().removesuffix('\n'))
    return refs


def validate_mount_binding(binding, case, context, session):
    if set(binding) != set(CHAIN_FILES) | {'schema', 'protocol', 'context', 'service', 'session', 'canonical_directory', 'daemon_id', 'probe'}:
        raise ValueError('strict mount binding keys')
    if binding['schema'] != 1 or binding['protocol'] != PROTOCOL or binding['context'] != context or binding['session'] != session:
        raise ValueError('mount binder context/session')
    service = binding['service']; canonical = str(Path(case)/'heap-control-evidence'/service)
    if service not in ('lantern-0','lantern-1','lantern-2') or binding['canonical_directory'] != canonical:
        raise ValueError('mount canonical service')
    directory = Path(case)/'heap-mount-evidence'/service
    mount_directory_bounds(directory)
    for key, name in CHAIN_FILES.items():
        if binding[key]['path'] != str(directory/name):
            raise ValueError('fixed node-local chain reference')
    if binding['probe'] is not None:
        if set(binding['probe']) != set(PROBE_FILES) or any(binding['probe'][key]['path'] != str(directory/name) for key,name in PROBE_FILES.items()):
            raise ValueError('fixed node-local probe reference')
    daemon_bytes(binding['daemon_id'])
    host = checked_mount_ref(binding['host_precreate_file'], case)
    if set(host) != {'device','inode','type','uid','gid','mode'} or any(not isinstance(host[k],str) or not re.fullmatch(r'[1-9][0-9]*',host[k]) for k in ('device','inode')) or any(type(host[k]) is not int or host[k]<0 for k in ('uid','gid','mode')) or host != host_identity(canonical) or host['type'] != 'directory' or host['mode'] != 448:
        raise ValueError('host directory identity changed')
    for key in ('host_before_file','host_after_file'):
        if checked_mount_ref(binding[key],case) != host: raise ValueError('host proof identity drift')
    for key in ('daemon_before_file','daemon_after_file'):
        if checked_mount_ref(binding[key],case,True,4096) != (binding['daemon_id']+'\n').encode(): raise ValueError('daemon proof drift')
    a = checked_mount_ref(binding['original_before_file'],case); b = checked_mount_ref(binding['original_after_file'],case)
    validate_observation(a); validate_observation(b)
    if stable_observation(a) != stable_observation(b) or any(v['state']['status'] != 'created' or v['state']['running'] is not False or v['restart_count'] != 0 or v['state']['started_at'] not in ('', '0001-01-01T00:00:00Z') or v['state']['pid'] != 0 for v in (a,b)):
        raise ValueError('original fingerprint/lifecycle drift')
    if a['id'] != session['container_id'] or a['image'] != context['image_id'] or a['user'] != context['runtime_user'] or a['project'] != context['run_id'] or a['service'] != service or a['name'] != '/' + context['run_id'] + '-' + service + '-1' or a['oneoff'] != 'False':
        raise ValueError('original session identity differs')
    sources,pairs = observed_sources(a,canonical)
    if checked_mount_ref(binding['pairs_file'],case) != pairs: raise ValueError('unproved source plan')
    validate_probe(binding['probe'],pairs,case,a)
    x = checked_mount_ref(binding['clone_before_file'],case); y = checked_mount_ref(binding['clone_after_file'],case)
    validate_observation(x); validate_observation(y)
    if stable_observation(x) != stable_observation(y) or x['state']['status'] != 'created' or x['state']['running'] is not False or y['state']['status'] != 'exited' or y['state']['running'] is not False or y['state']['exit_code'] != 0 or y['state']['oom_killed'] or x['restart_count'] or y['restart_count'] or x['state']['started_at'] not in ('', '0001-01-01T00:00:00Z') or x['state']['pid'] != 0 or y['state']['pid'] != 0:
        raise ValueError('clone fingerprint/lifecycle differs')
    if x['id'] == a['id'] or x['image'] != a['image'] or x['user'] != a['user'] or x['project'] != a['project'] or x['service'] != a['service'] or x['owner'] != 'compose-security-initializer' or x['oneoff'] != 'True' or x['entrypoint'] != ['/app/lantern'] or x['cmd'] != ['init-security'] or x['name'] != '/' + a['project'] + '-' + service + '-security-init' or not x['initializer'].startswith('lantern-owned-init.'):
        raise ValueError('clone identity/profile')
    q,r = heap_rows(x)
    if any(m['Source'] not in sources for m in (q,r)): raise ValueError('new clone representation; no second probe')
    aq,ar = heap_rows(a)
    if {k:v for k,v in q.items() if k!='Source'} != {k:v for k,v in aq.items() if k!='Source'} or {k:v for k,v in r.items() if k!='Source'} != {k:v for k,v in ar.items() if k!='Source'}:
        raise ValueError('clone nonSource options differ')
    require_created(a); require_created(b)
    check_clone(a,x,sources); check_clone(a,y,sources,True)
    return a,sources,host


def validate_observation(value):
    keys = {'schema','kind','id','name','image','user','project','service','oneoff','owner','initializer','entrypoint','cmd','working_dir','host_config','mounts','state','restart_count'}
    host_keys = {'network_mode','binds','mounts','readonly_rootfs','privileged','auto_remove','cap_add','cap_drop','security_opt','pid_mode','ipc_mode','uts_mode','userns_mode','restart_policy','tmpfs'}
    state_keys = {'status','running','pid','started_at','exit_code','oom_killed','restarting','paused'}
    if not isinstance(value,dict) or set(value)!=keys or type(value['schema']) is not int or value['schema']!=1 or value['kind']!='heap-mount-observation' or not isinstance(value['id'],str) or not HEX64.fullmatch(value['id']) or not isinstance(value['image'],str) or not re.fullmatch(r'sha256:[0-9a-f]{64}',value['image']):
        raise ValueError('invalid safe observation')
    if any(not isinstance(value[k],str) for k in ('name','user','project','service','oneoff','owner','initializer','working_dir')) or any(value[k] is not None and (not isinstance(value[k],list) or any(not isinstance(s,str) for s in value[k])) for k in ('entrypoint','cmd')):
        raise ValueError('safe observation scalar type')
    h,s=value['host_config'],value['state']
    if not isinstance(h,dict) or set(h)!=host_keys or not isinstance(s,dict) or set(s)!=state_keys or not isinstance(h['mounts'],list) or not isinstance(value['mounts'],list):
        raise ValueError('safe observation exact fields')
    if any(type(h[k]) is not bool for k in ('readonly_rootfs','privileged','auto_remove')) or any(type(s[k]) is not bool for k in ('running','oom_killed','restarting','paused')) or any(type(s[k]) is not int or s[k]<0 for k in ('pid','exit_code')) or type(value['restart_count']) is not int or value['restart_count']<0 or not isinstance(s['status'],str) or not isinstance(s['started_at'],str):
        raise ValueError('safe observation state types')


def daemon_bytes(value):
    if not isinstance(value,str) or not 0<len(value)<=128 or any(ord(c)<32 or ord(c)>126 for c in value):
        raise ValueError('bounded printable daemon identity')
    return (value+'\n').encode()


def write_raw_private(path,data,limit=MOUNT_LIMIT):
    if not isinstance(data,bytes) or not 0<len(data)<=limit:
        raise ValueError('bounded raw output')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    try:
        with os.fdopen(fd,'wb',closefd=False) as output:
            output.write(data); output.flush(); os.fsync(output.fileno())
    finally:
        os.close(fd)
    parent=os.open(Path(path).parent,os.O_RDONLY|os.O_DIRECTORY)
    try: os.fsync(parent)
    finally: os.close(parent)
    return {'path':str(path),'sha256':digest(data)}


def original_chain(root,service):
    stage=mount_stage(root,service); canonical=str(Path(root)/service)
    host=decode(read_private(stage/'host-precreate.json',MOUNT_LIMIT))
    if host!=host_identity(canonical): raise ValueError('host directory identity changed')
    for name in ('host-before.json','host-after.json'):
        if decode(read_private(stage/name,MOUNT_LIMIT))!=host: raise ValueError('host proof drift')
    a=decode(read_private(stage/'original-before.json',MOUNT_LIMIT)); b=decode(read_private(stage/'original-after.json',MOUNT_LIMIT))
    validate_observation(a); validate_observation(b)
    require_created(a); require_created(b)
    if a!=b: raise ValueError('original after proof changed')
    daemon=read_private(stage/'daemon-before',4096)
    if daemon_bytes(daemon.decode().removesuffix('\n'))!=daemon or read_private(stage/'daemon-after',4096)!=daemon:
        raise ValueError('daemon proof drift')
    sources,pairs=observed_sources(a,canonical)
    if decode(read_private(stage/'requested-pairs.json',MOUNT_LIMIT))!=pairs: raise ValueError('source plan drift')
    ref=lambda name:{'path':str(stage/name),'sha256':digest(read_private(stage/name,MOUNT_LIMIT))}
    probe=None if not pairs else {key:ref(name) for key,name in PROBE_FILES.items()}
    validate_probe(probe,pairs,Path(root).parent,a)
    return a,sources,host


def require_created(value):
    s=value['state']
    if s['status']!='created' or s['running'] is not False or s['pid']!=0 or s['started_at'] not in ('','0001-01-01T00:00:00Z') or s['oom_killed'] or s['paused'] or s['restarting'] or value['restart_count']!=0:
        raise ValueError('container was started or unstable')


def check_clone(original,value,sources,finished=False):
    validate_observation(value)
    req,eff=heap_rows(value); oq,om=heap_rows(original)
    if value['id']==original['id'] or any(value[k]!=original[k] for k in ('image','user','project','service')) or value['name']!='/'+original['project']+'-'+original['service']+'-security-init' or value['owner']!='compose-security-initializer' or value['oneoff']!='True' or not re.fullmatch(r'lantern-owned-init\.[A-Za-z0-9.-]+',value['initializer']) or value['entrypoint']!=['/app/lantern'] or value['cmd']!=['init-security'] or any(m['Source'] not in sources for m in (req,eff)):
        raise ValueError('clone differs from frozen original proof')
    if any({k:v for k,v in left.items() if k!='Source'}!={k:v for k,v in right.items() if k!='Source'} for left,right in ((req,oq),(eff,om))):
        raise ValueError('clone heap options changed')
    # The maintained Bash validator owns complete auth/Env/volume equality;
    # preserve its observed volumes/NoCopy here without persisting Env.
    if [m for m in value['host_config']['mounts'] if m.get('Target')!=TARGET] != [m for m in original['host_config']['mounts'] if m.get('Target')!=TARGET] or [m for m in value['mounts'] if m.get('Type')=='volume'] != [m for m in original['mounts'] if m.get('Type')=='volume']:
        raise ValueError('clone state volume options differ')
    if not finished: require_created(value)
    elif value['state']['status']!='exited' or value['state']['running'] is not False or value['state']['pid']!=0 or value['state']['exit_code']!=0 or any(value['state'][k] for k in ('oom_killed','paused','restarting')) or value['restart_count']!=0 or value['state']['started_at'] in ('','0001-01-01T00:00:00Z'):
        raise ValueError('clone did not finish successfully')


def binding_command(args,inspect=None):
    root=private_directory(args.directory); stage=mount_stage(root,args.service); canonical=root/args.service
    if inspect is None:
        data=sys.stdin.buffer.read(MOUNT_LIMIT+1)
        if len(data)>MOUNT_LIMIT: raise ValueError('bounded inspect input')
        inspect=decode(data)
    value=watched(inspect)
    host=decode(read_private(stage/'host-precreate.json',MOUNT_LIMIT))
    if host_identity(canonical)!=host or list(canonical.iterdir()): raise ValueError('precreate directory changed')
    if value['service']!=args.service: raise ValueError('fixed service identity')
    if args.command=='bind-original-before':
        require_created(value)
        if value['name']!='/'+value['project']+'-'+args.service+'-1' or value['oneoff']!='False': raise ValueError('original name differs')
        _,pairs=observed_sources(value,str(canonical))
        write_private(stage/'original-before.json',value,MOUNT_LIMIT); write_private(stage/'host-before.json',host)
        write_raw_private(stage/'daemon-before',daemon_bytes(args.daemon_id),4096)
        write_private(stage/'requested-pairs.json',pairs,MOUNT_LIMIT)
        return pairs
    if args.command=='bind-original-after':
        require_created(value)
        old=decode(read_private(stage/'original-before.json',MOUNT_LIMIT))
        if value!=old or read_private(stage/'daemon-before',4096)!=daemon_bytes(args.daemon_id): raise ValueError('original/daemon drift')
        # Completion means the exact stat premise has already been checked.
        sources,pairs=observed_sources(old,str(canonical))
        ref=lambda name:{'path':str(stage/name),'sha256':digest(read_private(stage/name,MOUNT_LIMIT))}
        validate_probe(None if not pairs else {k:ref(n) for k,n in PROBE_FILES.items()},pairs,root.parent,old)
        write_private(stage/'original-after.json',value,MOUNT_LIMIT); write_private(stage/'host-after.json',host)
        write_raw_private(stage/'daemon-after',daemon_bytes(args.daemon_id),4096)
        return None
    # All three original proofs must exist before accepting any clone.
    chains=[original_chain(root,f'lantern-{index}') for index in range(3)]
    if len({(chain[2]['device'],chain[2]['inode']) for chain in chains})!=3 or len({read_private(mount_stage(root,f'lantern-{index}')/'daemon-before',4096) for index in range(3)})!=1:
        raise ValueError('original cohort host/daemon mismatch')
    original,sources,_=original_chain(root,args.service)
    finished=args.command=='bind-clone-after'
    check_clone(original,value,sources,finished)
    if finished:
        prior=decode(read_private(stage/'clone-before.json',MOUNT_LIMIT))
        if stable_observation(prior)!=stable_observation(value): raise ValueError('clone fingerprint drift')
    write_private(stage/('clone-after.json' if finished else 'clone-before.json'),value,MOUNT_LIMIT)
    return None


def copy_mount_chain(root,node,context):
    service=Path(node['directory']).name
    stage=mount_stage(root,service); mount_directory_bounds(stage)
    pairs=decode(read_private(stage/'requested-pairs.json',MOUNT_LIMIT))
    names=list(CHAIN_FILES.values())+(list(PROBE_FILES.values()) if pairs else [])
    if set(p.name for p in stage.iterdir())!=set(names): raise ValueError('unexpected staging evidence')
    parent=Path(root).parent/'heap-mount-evidence'
    if not parent.exists():
        parent.mkdir(mode=0o700)
        os.chown(parent,os.getuid(),os.getgid(),follow_symlinks=False)
        os.chmod(parent,0o700)
    private_directory(parent)
    directory=parent/service; directory.mkdir(mode=0o700)
    os.chown(directory,os.getuid(),os.getgid(),follow_symlinks=False)
    os.chmod(directory,0o700)
    private_directory(directory)
    for name in names: write_raw_private(directory/name,read_private(stage/name,MOUNT_LIMIT))
    binding=chain_from_directory(directory,context,node['session'])
    validate_mount_binding(binding,Path(root).parent,context,node['session'])
    return binding


def read_binding(node,config):
    expected=Path(node['directory']).parent.parent/'heap-mount-evidence'/Path(node['directory']).name/'binding.json'
    if node['mount_binding_file']['path']!=str(expected): raise ValueError('fixed node binding path')
    binding=checked_mount_ref(node['mount_binding_file'],Path(node['directory']).parent.parent)
    if binding['service']!=Path(node['directory']).name: raise ValueError('binding node service differs')
    return validate_mount_binding(binding,Path(node['directory']).parent.parent,config['context'],node['session'])


def serving_profile(value,original,sources,state):
    validate_observation(value)
    s=value['state']
    if value['id']!=original['id'] or s['status']!=state or s['running'] is not (state=='running') or value['restart_count']!=0 or any(s[k] for k in ('oom_killed','paused','restarting')):
        raise ValueError('serving identity/lifecycle differs')
    if state=='created': require_created(value)
    elif s['pid']<=0 or not s['started_at'] or s['started_at']=='0001-01-01T00:00:00Z': raise ValueError('serving process missing')
    q,m=heap_rows(value)
    if q['Source'] not in sources or m['Source'] not in sources: raise ValueError('new serving Source')
    # Only the two proved heap Source representations may vary.
    def normalize(v):
        v=decode(json_bytes(v))
        request,effective=heap_rows(v)
        request['Source']='PROVED'; effective['Source']='PROVED'
        return stable_observation(v)
    if normalize(value)!=normalize(original): raise ValueError('serving immutable profile drift')


def inspect_nodes(config,state,deadline,runner=docker,phase=None,position=None):
    daemon=runner(['info','--format','{{.ID}}'],deadline)
    rows=[decode(line) for line in runner(['inspect','--format','{{json .}}',*[n['session']['container_id'] for n in config['nodes']]],deadline).splitlines()]
    if len(rows)!=3: raise ValueError('three exact containers required')
    refs=[]
    for node,raw_row in zip(config['nodes'],rows,strict=True):
        original,sources,host=read_binding(node,config)
        binding=checked_mount_ref(node['mount_binding_file'],Path(node['directory']).parent.parent)
        if daemon!=daemon_bytes(binding['daemon_id']): raise ValueError('target daemon changed')
        value=watched(raw_row); serving_profile(value,original,sources,state)
        if phase is not None:
            directory=Path(node['mount_binding_file']['path']).parent
            # Across both phases this exact VM process and start time stay fixed.
            first=directory/'baseline.before.inspect.json'
            if first.exists():
                prior=decode(read_private(first,MOUNT_LIMIT))
                if value['state']['pid']!=prior['state']['pid'] or value['state']['started_at']!=prior['state']['started_at']:
                    raise ValueError('serving process restarted')
            inspect_ref=write_private(directory/f'{phase}.{position}.inspect.json',value,MOUNT_LIMIT)
            daemon_ref=write_raw_private(directory/f'{phase}.{position}.daemon',daemon,4096)
            snapshot={'schema':1,'protocol':PROTOCOL,'phase':phase,'position':position,'service':original['service'],
                      'host_identity':host,'inspect_file':inspect_ref,'daemon_file':daemon_ref}
            refs.append(write_private(directory/f'{phase}.{position}.json',snapshot,MOUNT_LIMIT))
            mount_directory_bounds(directory)
    return refs


def initialize(root):
    root = Path(root)
    if not root.is_absolute() or root.parent.resolve() != root.parent:
        raise ValueError("control root path differs")
    # Only freshly created control directories are normalized. A setgid
    # parent may assign its group instead of the driver's primary group.
    for directory in (root, *(root / f"lantern-{index}" for index in range(3))):
        directory.mkdir(mode=0o700)
        os.chown(directory, os.getuid(), os.getgid(), follow_symlinks=False)
        os.chmod(directory, 0o700)
        private_directory(directory)

    stage=root.parent/'heap-mount-staging'
    stage.mkdir(mode=0o700)
    os.chown(stage,os.getuid(),os.getgid(),follow_symlinks=False); os.chmod(stage,0o700); private_directory(stage)
    for index in range(3):
        directory=stage/f'lantern-{index}'; directory.mkdir(mode=0o700)
        os.chown(directory,os.getuid(),os.getgid(),follow_symlinks=False); os.chmod(directory,0o700); private_directory(directory)
        write_private(directory/'host-precreate.json',host_identity(root/f'lantern-{index}'))


def prepare(args, runner=docker):
    root = private_directory(args.directory)
    out = root.parent
    containers = args.containers.split(",")
    endpoints = args.endpoints.split(",")
    if len(containers) != 3 or len(set(containers)) != 3 or any(not HEX64.fullmatch(c) for c in containers) or len(endpoints) != 3 or len(set(endpoints)) != 3:
        raise ValueError("invalid exact container/endpoint cohort")
    if not HEX40.fullmatch(args.source_commit) or not HEX40.fullmatch(args.source_tree) or not re.fullmatch(r"sha256:[0-9a-f]{64}", args.image_id) or not re.fullmatch(r"[1-9][0-9]*:[0-9]+", args.runtime_user) or int(args.runtime_user.split(":")[0]) != os.getuid():
        raise ValueError("invalid source/image/runtime identity")
    scenario = decode(read_private(args.scenario_json, 1 << 20, private=False))
    scenario_hash = digest(json.dumps(scenario, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode())
    config = {"schema": SCHEMA, "protocol": PROTOCOL, "context": {"run_id": args.run_id, "source_commit": args.source_commit, "source_tree": args.source_tree, "image_id": args.image_id, "scenario_sha256": scenario_hash, "runtime_user": args.runtime_user, "cpuset": args.cpuset}, "nodes": []}
    run_nonce = secrets.token_hex(32)
    for index, (cid, endpoint) in enumerate(zip(containers, endpoints, strict=True)):
        directory = private_directory(root / f"lantern-{index}")
        if list(directory.iterdir()):
            raise ValueError("control directory is not fresh")
        session = {"schema": SCHEMA, "protocol": PROTOCOL, "run_nonce": run_nonce, "source_commit": args.source_commit, "source_tree": args.source_tree, "image_id": args.image_id, "container_id": cid, "binary_sha256": "", "uid": os.getuid()}
        config["nodes"].append({"endpoint": endpoint, "directory": str(directory), "session": session})
    deadline = time.monotonic_ns() + CONTROLLER_NS
    bindings=[]
    for node in config['nodes']:
        bindings.append(copy_mount_chain(root,node,config['context']))
    hosts=[checked_mount_ref(b['host_precreate_file'],out) for b in bindings]
    if len({(h['device'],h['inode']) for h in hosts})!=3: raise ValueError('node host directories overlap')
    for node,binding in zip(config['nodes'],bindings,strict=True):
        # The final binding is sealed after binary hashing, before session writes.
        node['_binding']=binding
    # Validate actual never-started originals without any signal or session file.
    daemon=runner(['info','--format','{{.ID}}'],deadline)
    rows=[decode(line) for line in runner(['inspect','--format','{{json .}}',*containers],deadline).splitlines()]
    if len(rows)!=3: raise ValueError('three exact originals required')
    for node,row in zip(config['nodes'],rows,strict=True):
        binding=node['_binding']; original,sources,_=validate_mount_binding(binding,out,config['context'],node['session'])
        if daemon!=daemon_bytes(binding['daemon_id']): raise ValueError('prepare daemon changed')
        serving_profile(watched(row),original,sources,'created')
    binary = out / "heap-observer-binary"
    if binary.exists() or binary.is_symlink():
        raise ValueError("binary evidence already exists")
    runner(["cp", containers[0] + ":/app/lantern", str(binary)], deadline)
    info = binary.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.getuid() or not 0 < info.st_size <= BINARY_LIMIT:
        raise ValueError("binary evidence ownership/size differs")
    binary_hash = digest(binary.read_bytes())
    os.chmod(binary, 0o600)
    config["binary_file"] = {"path": str(binary), "sha256": binary_hash, "bytes": info.st_size}
    for node in config['nodes']:
        node['session']['binary_sha256']=binary_hash
        binding=node.pop('_binding'); binding['session']=node['session']
        validate_mount_binding(binding,out,config['context'],node['session'])
        node['mount_binding_file']=write_private(out/'heap-mount-evidence'/Path(node['directory']).name/'binding.json',binding,MOUNT_LIMIT)
    # Every node's premise and final binder is valid before the first session.
    for node in config["nodes"]:
        write_private(Path(node["directory"]) / "session.json", node["session"], INPUT_LIMIT)
    write_private(out / CONFIG_FILE, config)


def load_config(root):
    root = private_directory(root)
    config = decode(read_private(root.parent / CONFIG_FILE, OUTPUT_LIMIT))
    if config.get("schema") != SCHEMA or config.get("protocol") != PROTOCOL or len(config.get("nodes", [])) != 3:
        raise ValueError("control configuration differs")
    for index, node in enumerate(config["nodes"]):
        if node["directory"] != str(root / f"lantern-{index}"):
            raise ValueError("control configuration directory differs")
        private_directory(node["directory"])
        session, _ = raw_reference(Path(node["directory"]) / "session.json", INPUT_LIMIT)
        if session != node["session"]:
            raise ValueError("immutable session changed")
    return root, config


def completed_work(case, phase):
    counts = [0, 0]
    for stage in (("warmup",) if phase == "baseline" else ("warmup", "steady")):
        for index, name in enumerate(("receipt_admission", "receipt_lookup")):
            value = decode(read_private(case / f"ghz_{stage}_{index}_{name}.json", 1 << 20, private=False))
            count = value.get("count")
            if type(count) is not int or not 0 < count <= 8000 or value.get("statusCodeDistribution") != {"OK": count}:
                raise ValueError("workload did not complete successfully")
            counts[index] += count
    if counts[0] != counts[1] or counts[0] > 8000:
        raise ValueError("admission/status counts differ")
    return counts


def ready_request(node, phase, expected_receipts):
    directory = Path(node["directory"])
    ready, ready_ref = raw_reference(directory / "ready.json")
    if set(ready) != {"schema", "protocol", "identity"} or ready["schema"] != SCHEMA or ready["protocol"] != PROTOCOL:
        raise ValueError("invalid ready schema")
    identity = ready["identity"]
    if set(identity) != {"session", "pid", "start_nonce"} or identity["session"] != node["session"] or type(identity["pid"]) is not int or identity["pid"] != 1 or not HEX64.fullmatch(identity["start_nonce"]):
        raise ValueError("ready process identity differs")
    request = {"schema": SCHEMA, "protocol": PROTOCOL, "identity": identity, "phase": phase, "ordinal": 1 if phase == "baseline" else 2, "cut_nonce": secrets.token_hex(32), "expected_receipts": expected_receipts}
    if phase == "post":
        baseline, _ = raw_reference(directory / "baseline.complete.json")
        if baseline.get("status") != "complete" or baseline.get("request", {}).get("identity") != identity:
            raise ValueError("baseline missing or target restarted")
    elif (directory / "post.request.json").exists():
        raise ValueError("phase replay")
    return request, ready_ref


def validate_completion(value, request, expected_entries):
    if set(value) != {"schema", "protocol", "request", "status", "reason", "elapsed_ns", "before", "after", "collections", "memory"} or value["schema"] != 1 or value["protocol"] != PROTOCOL or value["request"] != request or value["status"] != "complete" or value["reason"] != "" or type(value["elapsed_ns"]) is not int or not 0 < value["elapsed_ns"] <= 25_000_000_000:
        raise ValueError("incomplete target cut")
    native = value["before"]
    native_keys = {"generation", "faulted", "pending_count", "pending_bytes", "pending_local", "snapshot_active", "origins", "log_sequence", "log_length", "log_capacity", "log_evicted", "receipt_entries", "receipt_bytes", "receipt_high_water_ms", "receipt_oldest_deadline_ms", "vertices", "edges", "public", "peer_unary", "peer_subscribe", "apply"}
    if set(native) != native_keys or native != value["after"] or any(native[k] is not False for k in ("faulted", "pending_local", "snapshot_active")) or native["pending_count"] != 0 or native["pending_bytes"] != 0 or native["receipt_entries"] < expected_entries:
        raise ValueError("native bracket drift/work mismatch")
    numeric = native_keys - {"faulted", "pending_local", "snapshot_active", "origins", "public", "peer_unary", "peer_subscribe", "apply"}
    if any(type(native[k]) is not int or not 0 <= native[k] < 1 << 64 for k in numeric) or not 0 < native["log_length"] <= native["log_capacity"] or native["receipt_high_water_ms"] <= 0 or native["receipt_oldest_deadline_ms"] <= native["receipt_high_water_ms"]:
        raise ValueError("invalid native counters")
    for name in ("public", "peer_unary", "peer_subscribe", "apply"):
        c = native[name]
        if set(c) != {"begun", "completed", "active"} or any(type(c[k]) is not int or c[k] < 0 for k in c) or c["begun"] - c["completed"] != c["active"] or (name != "peer_subscribe" and c["active"] != 0):
            raise ValueError("active native work")
    origins = native["origins"]
    if not isinstance(origins, list) or not 0 < len(origins) <= 16:
        raise ValueError("missing bounded origins")
    previous = ""
    for origin in origins:
        if set(origin) != {"id", "sequence", "wall_ns", "logical"} or not re.fullmatch(r"[0-9a-f]{32}", origin["id"]) or origin["id"] <= previous or type(origin["sequence"]) is not int or origin["sequence"] <= 0 or type(origin["wall_ns"]) is not int or origin["wall_ns"] <= 0 or type(origin["logical"]) is not int or not 0 <= origin["logical"] <= 0xffffffff:
            raise ValueError("invalid origin frontier")
        previous = origin["id"]
    def memory(m):
        keys = {"heap_alloc", "heap_objects", "heap_inuse", "num_gc", "num_forced_gc", "last_gc", "next_gc", "marked_live", "metric_cycles", "metric_forced", "cycle_after"}
        if set(m) != keys or any(type(m[k]) is not int or m[k] < 0 for k in keys) or m["num_gc"] != m["metric_cycles"] or m["num_gc"] != m["cycle_after"] or m["num_forced_gc"] != m["metric_forced"] or not 0 < m["heap_alloc"] <= (1 << 63) - 1 or m["marked_live"] <= 0:
            raise ValueError("incoherent memory read")
    collections = value["collections"]
    if not isinstance(collections, list) or len(collections) != 2:
        raise ValueError("two completed collections required")
    for index, gc in enumerate(collections):
        if set(gc) != {"started_ns", "returned_ns", "before", "after"} or type(gc["started_ns"]) is not int or type(gc["returned_ns"]) is not int or not 250_000_000 <= gc["started_ns"] < gc["returned_ns"] <= value["elapsed_ns"]:
            raise ValueError("collection return missing")
        memory(gc["before"])
        memory(gc["after"])
        if gc["after"]["num_gc"] <= gc["before"]["num_gc"] or gc["after"]["num_forced_gc"] - gc["before"]["num_forced_gc"] != 1 or gc["after"]["last_gc"] <= 0:
            raise ValueError("collection counters differ")
        if index and (gc["started_ns"] < collections[index-1]["returned_ns"] or gc["before"]["num_gc"] < collections[index-1]["after"]["num_gc"] or gc["before"]["num_forced_gc"] != collections[index-1]["after"]["num_forced_gc"]):
            raise ValueError("collection sequence drift")
    memory(value["memory"])
    if value["memory"] != collections[1]["after"] or collections[1]["after"]["num_forced_gc"] - collections[0]["before"]["num_forced_gc"] != 2:
        raise ValueError("immediate memory differs")


def capture(args, runner=docker, clock=time.monotonic_ns, pause=time.sleep):
    started = clock()
    deadline = started + CONTROLLER_NS
    root, config = load_config(args.directory)
    if not 0 < args.admission <= 8000 or args.lookup != args.admission:
        raise ValueError("completed admission/status counts required")
    result = {"schema": SCHEMA, "protocol": PROTOCOL, "phase": args.phase, "status": "incomplete", "controller_monotonic_start_ns": started, "controller_monotonic_end_ns": started, "context": dict(config["context"], workload={"admission": args.admission, "lookup": args.lookup}), "replicas": []}
    out = root.parent / ("retained_heap_pre.json" if args.phase == "baseline" else "retained_heap_post.json")
    try:
        scenario = decode(read_private(root.parent / "scenario.resolved.json", 1 << 20, private=False))
        if digest(json.dumps(scenario, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()) != config["context"]["scenario_sha256"] or completed_work(root.parent, args.phase) != [args.admission, args.lookup]:
            raise ValueError("scenario/workload context changed")
        before_refs=inspect_nodes(config,"running",deadline,runner,args.phase,"before")
        pending = []
        for node in config["nodes"]:
            request, ready_ref = ready_request(node, args.phase, args.admission)
            pending.append((node, request, ready_ref))
        # Persist every request before any daemon mutation. No signal is sent
        # when pre-dispatch file admission fails; existing files never replace.
        for node, request, _ in pending:
            directory = Path(node["directory"])
            if (directory / (args.phase + ".started.json")).exists() or (directory / (args.phase + ".complete.json")).exists() or (directory / "refusal.json").exists():
                raise ValueError("unexpected phase output exists")
            write_private(directory / (args.phase + ".request.json"), request, INPUT_LIMIT)
        signal_failures = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            futures = [pool.submit(runner, ["kill", "--signal=USR1", node["session"]["container_id"]], deadline) for node, _, _ in pending]
            for future in futures:
                try:
                    future.result()
                except (OSError, ValueError, TimeoutError):
                    signal_failures.append(True)
        for node, request, ready_ref in pending:
            directory = Path(node["directory"])
            if (directory / "refusal.json").exists() or (directory / "refusal.json").is_symlink():
                raise ValueError("target refused phase")
            complete_path = directory / (args.phase + ".complete.json")
            while not complete_path.exists() and clock() < deadline:
                if (directory / "refusal.json").exists():
                    raise ValueError("target refused phase")
                pause(min(0.05, max(0, (deadline - clock()) / 1e9)))
            completion, complete_ref = raw_reference(complete_path)
            begin, begin_ref = raw_reference(directory / (args.phase + ".started.json"))
            if completion.get("schema") != SCHEMA or completion.get("protocol") != PROTOCOL or completion.get("status") != "complete" or completion.get("request") != request or begin != {"request": request}:
                raise ValueError("target phase incomplete or identity differs")
            validate_completion(completion, request, args.admission)
            session, session_ref = raw_reference(directory / "session.json", INPUT_LIMIT)
            actual_request, request_ref = raw_reference(directory / (args.phase + ".request.json"), INPUT_LIMIT)
            if session != node["session"] or actual_request != request:
                raise ValueError("phase input changed")
            result["replicas"].append({"endpoint": node["endpoint"], "completion": completion, "session_file": session_ref, "ready_file": ready_ref, "request_file": request_ref, "started_file": begin_ref, "completion_file": complete_ref})
        after_refs=inspect_nodes(config,"running",deadline,runner,args.phase,"after")
        for replica,node,before,after in zip(result['replicas'],config['nodes'],before_refs,after_refs,strict=True):
            replica.update(mount_binding_file=node['mount_binding_file'],mount_inspect_before_file=before,mount_inspect_after_file=after)
        if any((Path(n["directory"]) / "refusal.json").exists() or (Path(n["directory"]) / "refusal.json").is_symlink() for n in config["nodes"]):
            raise ValueError("target refused phase")
        if any(n["completion"]["before"]["origins"] != result["replicas"][0]["completion"]["before"]["origins"] for n in result["replicas"]):
            raise ValueError("replica committed frontiers differ")
        if signal_failures or clock() >= deadline:
            raise ValueError("uncertain signal or controller deadline")
        # GC/native semantics are independently enforced by the Go evaluator
        # and final pair consumer, not inferred from CLI signal success.
        result["status"] = "complete"
    except (OSError, ValueError, KeyError, TypeError, TimeoutError):
        result["status"] = "incomplete"
    finally:
        result["controller_monotonic_end_ns"] = clock()
        write_private(out, result, 256 << 10)
    return 0 if result["status"] == "complete" else 1


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="command", required=True)
    init = sub.add_parser("init")
    init.add_argument("--directory", required=True)
    for command in ('bind-original-before','bind-original-after','bind-clone-before','bind-clone-after'):
        bind=sub.add_parser(command)
        bind.add_argument('--directory',required=True)
        bind.add_argument('--service',choices=('lantern-0','lantern-1','lantern-2'),required=True)
        if command.startswith('bind-original'):
            bind.add_argument('--daemon-id',required=True)
    prep = sub.add_parser("prepare")
    for name in ("directory", "containers", "endpoints", "source-commit", "source-tree", "image-id", "runtime-user", "scenario-json", "run-id"):
        prep.add_argument("--" + name, required=True)
    prep.add_argument("--cpuset", default="")
    cut = sub.add_parser("capture")
    cut.add_argument("--directory", required=True)
    cut.add_argument("--phase", choices=("baseline", "post"), required=True)
    cut.add_argument("--admission", type=int, required=True)
    cut.add_argument("--lookup", type=int, required=True)
    return p


def main(argv=None):
    args = parser().parse_args(argv)
    try:
        if args.command == "init":
            initialize(args.directory)
        elif args.command.startswith('bind-'):
            value=binding_command(args)
            if value is not None: print(json.dumps(value,separators=(',',':')))
        elif args.command == "prepare":
            prepare(args)
        else:
            return capture(args)
        return 0
    except (OSError, ValueError, KeyError, TypeError, TimeoutError):
        print("heap control incomplete; original owner retains cleanup custody", file=__import__("sys").stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
