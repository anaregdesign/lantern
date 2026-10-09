#!/usr/bin/env python3
"""Opt-in, bounded #1722 Linux campaign. Only new labeled task resources.

Use an already built static test binary, a cached immutable runtime image ID,
and the existing selected source config. No image pull, privilege or host change.
The retained named volume is evidence; this script never prunes Docker resources.
"""
import argparse
import datetime
import hashlib
import json
import pathlib
import subprocess
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=pathlib.Path, required=True)
parser.add_argument('--config', type=pathlib.Path, required=True)
parser.add_argument('--image', required=True)
parser.add_argument('--evidence', type=pathlib.Path, required=True)
parser.add_argument('--name', required=True)
parser.add_argument('--head', required=True)
parser.add_argument('--tree', required=True)
parser.add_argument('--cases', nargs='+', choices=['graceful', 'kill', 'pause-before', 'pause-after', 'peer-loss', 'time-loss'], default=['graceful', 'kill', 'pause-before', 'pause-after', 'peer-loss', 'time-loss'])
a = parser.parse_args()
if not a.image.startswith('sha256:') or len(a.image) != 71:
    parser.error('use the immutable cached image ID')
if not a.name.startswith('lantern-1722-') or not all(c.isalnum() or c == '-' for c in a.name):
    parser.error('task-specific resource name required')
a.evidence.mkdir(parents=True, exist_ok=False)
network, volume = a.name + '-net', a.name + '-state'
receipt = {'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'head': a.head, 'tree': a.tree, 'image': a.image, 'binary_sha256': hashlib.sha256(a.binary.read_bytes()).hexdigest(), 'config_sha256': hashlib.sha256(a.config.read_bytes()).hexdigest(), 'volume': volume, 'events': [], 'cases': {}, 'status': 'RUNNING'}
containers = []
created_network = False


def save():
    (a.evidence / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')


def docker(*args, check=True, record=True):
    p = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=45)
    if record:
        receipt['events'].append({'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'args': args, 'exit': p.returncode, 'stdout': p.stdout, 'stderr': p.stderr})
        save()
    if check and p.returncode:
        raise RuntimeError(f'docker {args}: {p.stderr}')
    return p


def inspect(name):
    return json.loads(docker('inspect', name, record=False).stdout)[0]


def marker(name, path, timeout=170):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not inspect(name)['State']['Running']:
            raise RuntimeError(f'{name} exited before {path}')
        if docker('exec', name, 'test', '-f', path, check=False, record=False).returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError(f'{name} did not reach {path}')


def finish(name, timeout=220):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        state = inspect(name)['State']
        if not state['Running']:
            if state['ExitCode'] != 0:
                raise RuntimeError(f'{name}: test exit {state["ExitCode"]}')
            return
        time.sleep(1)
    raise RuntimeError(f'{name}: bounded finish timed out')


def touch(name, case, marker_name):
    docker('exec', name, 'touch', f'/state/{case}/{marker_name}.json')


def collect(name, case, suffix):
    p = docker('logs', name, check=False, record=False)
    (a.evidence / f'{case}-{suffix}.log').write_text(p.stdout + p.stderr)
    receipt['cases'][case][suffix + '_inspect'] = inspect(name)
    destination = a.evidence / f'{case}-{suffix}'
    destination.mkdir(exist_ok=True)
    # Deliberately leave test private keys only in the task-owned volume.
    for item in ['bootstrap.json', 'checkpoint.json', 'ready.json', 'authorization.json', 'stop-request.json', 'graceful-close.json', 'restart-result.json', 'boundary-result.json', 'fault-result.json', 'recovery-result.json']:
        docker('cp', f'{name}:/state/{case}/{item}', str(destination / item), check=False, record=False)
    save()


def start(case, name):
    phase = 'pre-stop' if case in ('graceful', 'kill') else case
    # exec makes the test worker PID 1. Restart chooses explicit resume of the
    # exact persisted case. Missing/corrupt bootstrap is never fresh fallback.
    command = f'if test -f /state/{case}/checkpoint.json; then export LANTERN_CONTAINER_PHASE=resume-process; fi; exec /test -test.v -test.run=^TestContainerCurrentAuthority$ -test.timeout=8m'
    docker('run', '-d', '--pull=never', '--name', name, '--label', 'lantern.task=1722-linux-matrix', '--network', network, '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--user', '65534:65534', '--pids-limit', '256', '--memory', '1g', '--cpus', '2', '--tmpfs', '/tmp:rw,nosuid,nodev,size=64m,mode=1777', '--mount', f'type=volume,src={volume},dst=/state', '--mount', f'type=bind,src={a.binary.resolve()},dst=/test,readonly', '--mount', f'type=bind,src={a.config.resolve()},dst=/etc/ntp.conf,readonly', '--env', 'LANTERN_CONTAINER_MODE=container', '--env', f'LANTERN_CONTAINER_DIR=/state/{case}', '--env', f'LANTERN_CONTAINER_PHASE={phase}', '--env', f'LANTERN_CONTAINER_HEAD={a.head}', '--env', f'LANTERN_CONTAINER_TREE={a.tree}', a.image, '/bin/sh', '-c', command)
    containers.append((name, case))


try:
    docker('image', 'inspect', a.image)
    docker('network', 'create', '--label', 'lantern.task=1722-linux-matrix', network)
    created_network = True
    docker('volume', 'create', '--label', 'lantern.task=1722-linux-matrix', volume)
    # No capabilities: root owns only this newly created volume directory and
    # sets its mode. Product workers always run uid/gid 65534, caps empty.
    docker('run', '--rm', '--pull=never', '--network', 'none', '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--mount', f'type=volume,src={volume},dst=/state', a.image, 'chmod', '1777', '/state')
    for case in a.cases:
        print('START', case, flush=True)
        name = a.name + '-' + case
        receipt['cases'][case] = {'status': 'RUNNING'}
        start(case, name)
        marker(name, f'/state/{case}/ready.json')
        print('READY', case, flush=True)
        if case == 'graceful':
            docker('stop', '--timeout', '20', name)
            if inspect(name)['State']['ExitCode'] != 0:
                raise RuntimeError('graceful stop did not join successfully')
            collect(name, case, 'stopped')
            if not (a.evidence / f'{case}-stopped/graceful-close.json').exists():
                raise RuntimeError('graceful Close receipt missing')
            docker('start', name)
        elif case == 'kill':
            docker('kill', '--signal', 'KILL', name)
            if inspect(name)['State']['ExitCode'] != 137:
                raise RuntimeError('SIGKILL exit not observed')
            collect(name, case, 'killed')
            docker('rm', name)
            containers.remove((name, case))
            name += '-resumed'
            start(case, name)
        elif case.startswith('pause-'):
            docker('pause', name)
            if not inspect(name)['State']['Paused']:
                raise RuntimeError('Docker pause not observed')
            before = time.monotonic_ns()
            # Independent controller continues while worker is frozen. 77s
            # includes margin over the 75s conservative lower assertion.
            time.sleep(77)
            docker('unpause', name)
            receipt['cases'][case]['controller_pause_ns'] = time.monotonic_ns() - before
            touch(name, case, 'release')
        else:
            if case == 'time-loss':
                docker('network', 'disconnect', network, name)
            before = time.monotonic_ns()
            time.sleep(77 if case == 'time-loss' else 18)
            receipt['cases'][case]['controller_outage_ns'] = time.monotonic_ns() - before
            touch(name, case, 'check')
            marker(name, f'/state/{case}/fault-result.json', timeout=15)
            if case == 'time-loss':
                docker('network', 'connect', network, name)
            touch(name, case, 'release')
        finish(name)
        collect(name, case, 'finished')
        receipt['cases'][case]['status'] = 'PASS'
        print('PASS', case, flush=True)
        docker('rm', name)
        containers.remove((name, case))
    receipt['status'] = 'PASS'
except BaseException as exc:
    receipt['status'], receipt['error'] = 'FAIL', repr(exc)
    print('FAIL', repr(exc), flush=True)
finally:
    # Cleanup is explicit and limited to names successfully created by this run.
    for name, case in containers:
        try:
            state = inspect(name)['State']
            if state.get('Paused'):
                docker('unpause', name, check=False)
            if state.get('Running'):
                docker('kill', name, check=False)
            collect(name, case, 'failure')
            docker('rm', name, check=False)
        except Exception as exc:
            receipt.setdefault('cleanup_errors', []).append(repr(exc))
    if created_network:
        docker('network', 'rm', network, check=False)
    receipt['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    receipt['volume_retained'] = True
    save()
raise SystemExit(0 if receipt['status'] == 'PASS' else 1)
