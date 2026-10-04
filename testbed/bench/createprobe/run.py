#!/usr/bin/env python3
"""Own one native standalone fixture per declared Create family, with bounded teardown."""
import hashlib
import json
import os
from pathlib import Path
import selectors
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
HERE = ROOT / 'testbed/bench'

def seconds(value):
    if not isinstance(value, str) or not value.endswith('s'):
        raise ValueError('bounded seconds required')
    result = float(value[:-1])
    if not 0 < result <= 300:
        raise ValueError('duration out of bounds')
    return result

def validate(config):
    if config['name'] != 'edge_create' or config['target']['driver'] != 'standalone_create':
        raise ValueError('standalone Create scenario required')
    if config['cluster']['topology'] != 'standalone' or config['cluster']['auth_mode'] != 'oidc':
        raise ValueError('standalone OIDC required')
    if config['target']['families'] != ['plain', 'receipt']:
        raise ValueError('complete declared family order required')
    wal = config['cluster']['receipt_wal']
    if wal != {'enabled': True, 'max_entries': 32768, 'max_bytes': 16777216}:
        raise ValueError('frozen native fixture caps required')
    for phase in ('warmup', 'steady'):
        block = config['phases'][phase]
        seconds(block['duration'])
        if block['concurrency'] != 1 or block['rps'] != 100:
            raise ValueError('declared workload cannot be silently changed')
    seconds(config['phases']['cooldown'])
    return config

def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def source_digest():
    names = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=ROOT).split(b'\0')
    total = hashlib.sha256()
    for raw in sorted(set(names)):
        if not raw:
            continue
        path = Path(os.fsdecode(raw))
        if path.suffix not in ('.go', '.proto', '.mod', '.sum', '.work') and not str(path).startswith('testbed/bench/createprobe/'):
            continue
        if not (ROOT / path).is_file() or path.parts[:3] == ('testbed', 'bench', 'out') or '__pycache__' in path.parts:
            continue
        total.update(raw + b'\0' + (ROOT/path).read_bytes() + b'\0')
    return total.hexdigest()

def read_ready(process):
    watcher = selectors.DefaultSelector()
    watcher.register(process.stdout, selectors.EVENT_READ)
    try:
        if not watcher.select(90):
            raise RuntimeError('native certified readiness timed out')
        line = process.stdout.readline()
        if not line:
            raise RuntimeError('native certified readiness failed')
        return json.loads(line)
    finally:
        watcher.close()

def rss(process):
    # Read only PIDs/RSS and retain only the owned supervisor's direct children.
    rows = subprocess.check_output(['ps', '-axo', 'pid=,ppid=,rss='], text=True)
    return sum(int(parts[2])*1024 for row in rows.splitlines() if len(parts := row.split()) == 3 and int(parts[1]) == process.pid)

def metrics(port):
    wanted = {'go_memstats_heap_alloc_bytes', 'go_goroutines', 'go_gc_duration_seconds_count'}
    with urllib.request.urlopen(f'http://127.0.0.1:{port}/metrics', timeout=3) as response:
        body = response.read(1 << 20).decode('utf8')
    return {parts[0]: float(parts[1]) for row in body.splitlines() if len(parts := row.split()) == 2 and parts[0] in wanted}

def stop(process):
    if process.stdin and not process.stdin.closed:
        # The shared native supervisor intentionally ignores EOF, so detached
        # CI launchers do not terminate the cohort. Use its explicit command.
        try:
            process.stdin.write(b'shutdown\n')
            process.stdin.flush()
        except BrokenPipeError:
            pass
        process.stdin.close()
    try:
        return process.wait(timeout=30) == 0
    except subprocess.TimeoutExpired:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)
        return False

def family(name, config, binaries, output):
    private = Path(tempfile.mkdtemp(prefix='lantern-create-native-'))
    os.chmod(private, 0o700)
    native = None
    driver = None
    teardown = False
    try:
        token = 'lnt_m1_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE'
        credentials = private/'credentials.json'
        credentials.write_text(json.dumps([token])); os.chmod(credentials, 0o600)
        token_file = private/'token'
        token_file.write_text(token); os.chmod(token_file, 0o600)
        public = socket.socket(); public.bind(('127.0.0.1', 0))
        operator = socket.socket(); operator.bind(('127.0.0.1', 0))
        public_port = public.getsockname()[1]; operator_port = operator.getsockname()[1]
        overrides = private/'overrides.json'
        overrides.write_text(json.dumps([{'LANTERN_METRICS_ADDR': f'127.0.0.1:{operator_port}', 'LANTERN_LOG_LEVEL': 'warn'}])); os.chmod(overrides, 0o600)
        public.close(); operator.close()
        with (private/'fixture.log').open('wb') as error_log:
            os.chmod(private/'fixture.log', 0o600)
            native = subprocess.Popen([str(binaries['fixture']), '-directory', str(private/'trust'), '-mode', 'oidc', '-public-ports', str(public_port), '-peer-ports', '', '-receipt', '-edge-create', '-overrides-file', str(overrides), '-tokens-file', str(credentials), '-serve', str(binaries['server'])], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=error_log)
            metadata = read_ready(native)
            if len(metadata['nodes']) != 1 or metadata['nodes'][0]['peer_origin']:
                raise RuntimeError('native topology mismatch')
            before_rss = rss(native); peak = before_rss; before_metrics = metrics(operator_port)
            args = [str(binaries['probe']), '-endpoint', metadata['nodes'][0]['public_origin'], '-ca-file', metadata['ca_file'], '-token-file', str(token_file), '-warmup', config['phases']['warmup']['duration'], '-duration', config['phases']['steady']['duration'], '-rps', '100', '-out', str(output/f'{name}.json')]
            if name == 'receipt':
                args.append('-receipt')
            with (output/f'{name}.log').open('wb') as driver_log:
                driver = subprocess.Popen(args, stdout=driver_log, stderr=subprocess.STDOUT)
                deadline = time.monotonic() + seconds(config['phases']['warmup']['duration']) + seconds(config['phases']['steady']['duration']) + 60
                while driver.poll() is None:
                    if time.monotonic() > deadline:
                        raise RuntimeError('Create phase timed out')
                    peak = max(peak, rss(native))
                    time.sleep(.1)
                if driver.returncode:
                    raise RuntimeError('Create semantic/transport gate failed')
            time.sleep(seconds(config['phases']['cooldown']))
            after_metrics = metrics(operator_port); after_rss = rss(native)
            teardown = stop(native)
            if not teardown:
                raise RuntimeError('native teardown failed')
            report = {'rss_before_bytes': before_rss, 'rss_peak_sampled_during_driver_bytes': peak, 'rss_after_cooldown_bytes': after_rss, 'natural_runtime_before': before_metrics, 'natural_runtime_after': after_metrics, 'teardown': 'pass'}
            (output/f'{name}-runtime.json').write_text(json.dumps(report, indent=2))
        # Trust/credential/Server state is private and never part of public artifacts.
        import shutil
        shutil.rmtree(private)
    except BaseException as failure:
        failed = private/'failure.json'
        failed.write_text(json.dumps({'type': type(failure).__name__, 'message': str(failure)}))
        os.chmod(failed, 0o600)
        if driver and driver.poll() is None:
            driver.terminate()
            try:
                driver.wait(timeout=5)
            except subprocess.TimeoutExpired:
                driver.kill(); driver.wait(timeout=5)
        if native and not teardown:
            stop(native)
        print(f'createprobe: private failed-run evidence retained at {private}', file=sys.stderr)
        raise

def main():
    if len(sys.argv) != 2:
        raise ValueError('one scenario path required')
    config = validate(json.loads(subprocess.check_output(['yq', '-o=json', '.', sys.argv[1]])))
    output = HERE/'out/edge_create'/time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())
    output.mkdir(parents=True, mode=0o700)
    before = source_digest()
    binaries = {key: output/key for key in ('server', 'fixture', 'probe')}
    for key, directory, package in [('server', ROOT/'server', './cmd'), ('fixture', ROOT/'server', './cmd/authfixture'), ('probe', ROOT, './testbed/bench/createprobe')]:
        subprocess.run(['go', 'build', '-o', str(binaries[key]), package], cwd=directory, check=True)
    provenance = {'schema': 1, 'qualification': 'local_dirty_branch_diagnostic', 'source_sha256': before, 'binary_sha256': {key: digest(path) for key, path in binaries.items()}, 'scenario_sha256': digest(sys.argv[1]), 'forced_gc': False}
    for name in config['target']['families']:
        family(name, config, binaries, output)
    if before != source_digest():
        raise RuntimeError('runtime source changed during declared family')
    provenance['verdict'] = 'pass'
    (output/'provenance.json').write_text(json.dumps(provenance, indent=2))
    for path in binaries.values():
        path.unlink()
    print(f'createprobe: diagnostic evidence at {output}')

if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('createprobe: diagnostic family is unqualified', file=sys.stderr)
        sys.exit(1)
