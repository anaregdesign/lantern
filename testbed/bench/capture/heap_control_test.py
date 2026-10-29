"""Deterministic file/daemon-control tests; never start Docker or collect GC."""
import argparse
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import heap_control as control


def completion(request, heap=100 << 20):
    count = request["expected_receipts"]
    counter = {"begun": count, "completed": count, "active": 0}
    native = {"generation": count, "faulted": False, "pending_count": 0, "pending_bytes": 0, "pending_local": False, "snapshot_active": False,
              "origins": [{"id": f"{i:032x}", "sequence": count, "wall_ns": 1_793_000_000_000_000_000, "logical": 0} for i in range(1, 4)],
              "log_sequence": count, "log_length": count, "log_capacity": 10000, "log_evicted": 0, "receipt_entries": count, "receipt_bytes": count * 256,
              "receipt_high_water_ms": 1000, "receipt_oldest_deadline_ms": 3_601_000, "vertices": 4, "edges": 0,
              "public": counter, "peer_unary": counter, "apply": counter, "peer_subscribe": {"begun": 2, "completed": 0, "active": 2}}
    def memory(gc, forced):
        return {"heap_alloc": heap, "heap_objects": 1000, "heap_inuse": heap + 1024, "num_gc": gc, "num_forced_gc": forced, "last_gc": gc * 1_000_000,
                "next_gc": heap * 2, "marked_live": heap - 1024, "metric_cycles": gc, "metric_forced": forced, "cycle_after": gc}
    forced = 0 if request["phase"] == "baseline" else 2
    gc = 10 if request["phase"] == "baseline" else 20
    collections = [{"started_ns": 300_000_000, "returned_ns": 400_000_000, "before": memory(gc, forced), "after": memory(gc+1, forced + 1)},
                   {"started_ns": 500_000_000, "returned_ns": 600_000_000, "before": memory(gc+1, forced + 1), "after": memory(gc+2, forced + 2)}]
    return {"schema": 1, "protocol": control.PROTOCOL, "request": request, "status": "complete", "reason": "", "elapsed_ns": 900_000_000,
            "before": native, "after": copy.deepcopy(native), "collections": collections, "memory": memory(gc+2, forced + 2)}


def inspect_row(root,index,clone=False):
    service=f'lantern-{index}'; project='owned-test'; image='sha256:'+'c'*64
    canonical=str(root/service)
    requests=[{'Type':'volume','Source':f'{project}_state-{index}','Target':'/state','VolumeOptions':{'NoCopy':True}},
              {'Type':'volume','Source':f'{project}_backup-{index}','Target':'/data','VolumeOptions':{'NoCopy':True}},
              {'Type':'bind','Source':canonical,'Target':control.TARGET_DIRECTORY,'ReadOnly':False,'BindOptions':{'Propagation':'rprivate'}}]
    mounts=[{'Type':'bind','Source':'/private/config','Destination':'/run/lantern-config','Mode':'ro','RW':False,'Propagation':'rprivate'},
            {'Type':'bind','Source':'/private/config/ntp.conf','Destination':'/etc/ntp.conf','Mode':'ro','RW':False,'Propagation':'rprivate'}]
    for name,target in (('state','/state'),('backup','/data')):
        volume=f'{project}_{name}-{index}'
        mounts.append({'Type':'volume','Source':'/var/lib/docker/volumes/'+volume+'/_data','Destination':target,'Mode':'z','RW':True,'Propagation':'','Name':volume,'Driver':'local'})
    mounts.append({'Type':'bind','Source':canonical,'Destination':control.TARGET_DIRECTORY,'Mode':'','RW':True,'Propagation':'rprivate'})
    labels={'com.docker.compose.project':project,'com.docker.compose.service':service,'com.docker.compose.oneoff':'True' if clone else 'False'}
    if clone: labels.update({'lantern.owner':'compose-security-initializer','lantern.initializer':'lantern-owned-init.test'+str(index)})
    return {'Id':f'{index+(11 if clone else 1):064x}','Name':'/'+project+'-'+service+('-security-init' if clone else '-1'),'Image':image,
            'Config':{'Image':image,'User':f'{os.getuid()}:{os.getgid()}','Labels':labels,'Entrypoint':['/app/lantern'],'Cmd':['init-security'] if clone else None,'WorkingDir':'/app','Env':['DO_NOT_PERSIST=secret-sentinel'],'LogConfig':{'private':'secret'}},
            'HostConfig':{'NetworkMode':project+'_default','Binds':['/private/config:/run/lantern-config:ro','/private/config/ntp.conf:/etc/ntp.conf:ro'],'Mounts':requests,'ReadonlyRootfs':False,'Privileged':False,'AutoRemove':False,'CapAdd':None,'CapDrop':None,'SecurityOpt':None,'PidMode':'','IpcMode':'private','UTSMode':'','UsernsMode':'','RestartPolicy':{'Name':'no','MaximumRetryCount':0},'Tmpfs':None},
            'Mounts':mounts,'State':{'Status':'created','Running':False,'Pid':0,'StartedAt':'0001-01-01T00:00:00Z','ExitCode':0,'OOMKilled':False,'Restarting':False,'Paused':False},'RestartCount':0}


def bind_args(root,index,command):
    return argparse.Namespace(directory=str(root),service=f'lantern-{index}',command=command,daemon_id='test-daemon')


def write_probe(root,index,pairs):
    stage=control.mount_stage(root,f'lantern-{index}')
    row=inspect_row(root,index); project='owned-test'; service=f'lantern-{index}-bind-proof'; cid=f'{index+21:064x}'
    targets=[f'/proof/{side}-{i}' for i in range(len(pairs)) for side in ('original','clone')]
    requests=[]; mounts=[]
    for i,pair in enumerate(pairs):
        for side in ('original','clone'):
            target=f'/proof/{side}-{i}'; source=pair[side+'_source']
            requests.append({'Type':'bind','Source':source,'Target':target,'ReadOnly':True,'BindOptions':{'Propagation':'rprivate'}})
            mounts.append({'Type':'bind','Source':source,'Destination':target,'Mode':'ro','RW':False,'Propagation':'rprivate'})
    env=['PATH=/usr/local/bin:/usr/bin:/bin']
    row.update(Id=cid,Name='/'+project+'-'+service)
    row['Config'].update(Env=env,Entrypoint=['/bin/busybox'],Cmd=['stat','-c','%d:%i:%F',*targets],Labels={'com.docker.compose.project':project,'com.docker.compose.service':service,'com.docker.compose.oneoff':'True','lantern.owner':'compose-bind-identity-probe','lantern.initializer':'lantern-owned-init.probe'+str(index)})
    row['HostConfig']={'NetworkMode':'none','ReadonlyRootfs':True,'Privileged':False,'AutoRemove':False,'CapDrop':['ALL'],'SecurityOpt':['no-new-privileges:true'],'Memory':33554432,'NanoCpus':250000000,'PidsLimit':16,'PidMode':'','UTSMode':'','UsernsMode':'','PublishAllPorts':False,'CapAdd':None,'RestartPolicy':{'Name':'no','MaximumRetryCount':0},'Mounts':requests}
    row['Mounts']=mounts
    control.write_private(stage/'probe-created.json',row,control.MOUNT_LIMIT)
    after=copy.deepcopy(row); after['State'].update(Status='exited',StartedAt='2026-10-30T00:00:00Z')
    control.write_private(stage/'probe-completed.json',after,control.MOUNT_LIMIT)
    control.write_private(stage/'image-environment.json',env,control.MOUNT_LIMIT)
    control.write_raw_private(stage/'probe-output.log',b''.join((f'{10+i}:{20+i}:directory\n'*2).encode() for i in range(len(pairs))))
    control.write_raw_private(stage/'probe-wait.log',b'0\n')
    control.write_raw_private(stage/'probe-remove.log',(cid+'\n').encode())


def stage_chain(root,rows):
    for index,row in enumerate(rows):
        pairs=control.binding_command(bind_args(root,index,'bind-original-before'),row)
        if pairs: write_probe(root,index,pairs)
        control.binding_command(bind_args(root,index,'bind-original-after'),row)
    for index in range(3):
        row=inspect_row(root,index,True)
        # Clone may use the original's already proved representation.
        row['HostConfig']['Mounts'][-1]['Source']=rows[index]['HostConfig']['Mounts'][-1]['Source']
        row['Mounts'][-1]['Source']=rows[index]['Mounts'][-1]['Source']
        control.binding_command(bind_args(root,index,'bind-clone-before'),row)
        row['State'].update(Status='exited',StartedAt='2026-10-30T00:00:00Z')
        control.binding_command(bind_args(root,index,'bind-clone-after'),row)


class FakeDocker:
    def __init__(self, root):
        self.root = root
        self.calls = []
        self.state = "created"
        self.config = None
        self.damage = None
        self.signal_error = False
        self.all_requests_committed = True
        self.rows=[inspect_row(root,index) for index in range(3)]
        self.inspect_damage=None
        self.daemon=b'test-daemon\n'

    def __call__(self, argv, deadline):
        self.calls.append(list(argv))
        if argv[0] == 'info': return self.daemon
        if argv[0] == "inspect":
            rows=[]
            for cid in argv[3:]:
                row=copy.deepcopy(next(r for r in self.rows if r['Id']==cid))
                if self.state=='running': row['State'].update(Status='running',Running=True,Pid=1000+int(cid,16),StartedAt='2026-10-29T00:00:00Z')
                if self.inspect_damage: self.inspect_damage(row)
                rows.append(row)
            return b"\n".join(control.json_bytes(row).strip() for row in rows)
        if argv[0] == "cp":
            Path(argv[2]).write_bytes(b"owned immutable fake executable")
            return b""
        if argv[:2] == ["kill", "--signal=USR1"]:
            for directory in self.root.iterdir():
                self.all_requests_committed &= (directory / "baseline.request.json").exists() or (directory / "post.request.json").exists()
            config = control.decode(control.read_private(self.root.parent / control.CONFIG_FILE, control.OUTPUT_LIMIT))
            node = next(n for n in config["nodes"] if n["session"]["container_id"] == argv[2])
            directory = Path(node["directory"])
            phase = "post" if (directory / "post.request.json").exists() else "baseline"
            request = control.decode(control.read_private(directory / f"{phase}.request.json", control.INPUT_LIMIT))
            value = completion(request)
            if self.damage:
                self.damage(value)
            control.write_private(directory / f"{phase}.started.json", {"request": request})
            control.write_private(directory / f"{phase}.complete.json", value)
            if self.signal_error:
                raise TimeoutError("uncertain signal")
            return (argv[2] + "\n").encode()
        raise AssertionError(argv)


class HeapControlTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.case = Path(self.temp.name).resolve()
        self.root = self.case / "heap-control-evidence"
        control.initialize(self.root)
        self.scenario = self.case / "scenario.resolved.json"
        self.scenario.write_text('{"name":"receipt","phases":{"steady":{"duration":"45s"}}}')
        for phase, count in (("warmup", 1000), ("steady", 4500)):
            for index, name in enumerate(("receipt_admission", "receipt_lookup")):
                (self.case / f"ghz_{phase}_{index}_{name}.json").write_text(json.dumps({"count": count, "statusCodeDistribution": {"OK": count}}))
        self.runner = FakeDocker(self.root)
        self.args = argparse.Namespace(directory=str(self.root), containers=",".join(f"{i:064x}" for i in range(1, 4)), endpoints=",".join(f"localhost:{9390+i}" for i in range(3)),
                                       source_commit="a" * 40, source_tree="b" * 40, image_id="sha256:" + "c" * 64, runtime_user=f"{os.getuid()}:{os.getgid()}",
                                       scenario_json=str(self.scenario), run_id="owned-test", cpuset="0-3")
        stage_chain(self.root,self.runner.rows)

    def tearDown(self):
        self.temp.cleanup()

    def prepare(self):
        control.prepare(self.args, self.runner)
        self.runner.state = "running"
        _, config = control.load_config(self.root)
        for index, node in enumerate(config["nodes"]):
            control.write_private(Path(node["directory"]) / "ready.json", {"schema": 1, "protocol": control.PROTOCOL,
                                  "identity": {"session": node["session"], "pid": 1, "start_nonce": f"{index+100:064x}"}})
        return config

    def cut_args(self, phase="baseline"):
        return argparse.Namespace(directory=str(self.root), phase=phase, admission=1000 if phase == "baseline" else 5500, lookup=1000 if phase == "baseline" else 5500)

    def test_initialize_normalizes_only_new_private_control_directories(self):
        parent=self.case/"new-case"; parent.mkdir(mode=0o700)
        root = parent / "new-control"
        original_chown = os.chown
        with mock.patch.object(control.os, "chown", wraps=original_chown) as ownership:
            control.initialize(root)
            stage=parent/"heap-mount-staging"
            expected = [root, *(root / f"lantern-{index}" for index in range(3)),stage,*(stage/f"lantern-{index}" for index in range(3))]
            self.assertEqual(ownership.call_args_list, [mock.call(path, os.getuid(), os.getgid(), follow_symlinks=False) for path in expected])
            for directory in expected:
                info = directory.lstat()
                self.assertEqual((info.st_uid, info.st_gid, info.st_mode & 0o7777), (os.getuid(), os.getgid(), 0o700))
            with self.assertRaises(FileExistsError):
                control.initialize(root)
            self.assertEqual(ownership.call_count, 8)

    def test_prepare_reads_three_never_started_bindings_and_copies_one_binary(self):
        self.prepare()
        self.assertEqual([call[0] for call in self.runner.calls], ["info", "inspect", "cp"])
        self.assertTrue((self.case / "heap-observer-binary").is_file())
        with self.assertRaises(ValueError):
            control.prepare(self.args, self.runner)
        self.assertEqual([call[0] for call in self.runner.calls], ["info", "inspect", "cp"])

    def test_prepare_normalizes_only_new_mount_evidence_directories(self):
        original_chown=os.chown
        with mock.patch.object(control.os,'chown',wraps=original_chown) as ownership:
            self.prepare()
        parent=self.case/'heap-mount-evidence'
        expected=[parent,*(parent/f'lantern-{index}' for index in range(3))]
        self.assertEqual(ownership.call_args_list,[mock.call(path,os.getuid(),os.getgid(),follow_symlinks=False) for path in expected])
        for path in expected:
            info=path.lstat()
            self.assertEqual((info.st_uid,info.st_gid,info.st_mode & 0o7777),(os.getuid(),os.getgid(),0o700))

    def test_prepare_does_not_normalize_an_existing_untrusted_proof_directory(self):
        parent=self.case/'heap-mount-evidence'; parent.mkdir(mode=0o700); parent.chmod(0o755)
        with mock.patch.object(control.os,'chown',wraps=os.chown) as ownership:
            with self.assertRaises(ValueError): control.prepare(self.args,self.runner)
        self.assertFalse(ownership.called)
        self.assertEqual(parent.lstat().st_mode & 0o7777,0o755)
        self.assertFalse(any(self.root.rglob('session.json')))
        self.assertFalse(self.runner.calls)

    def test_full_phase_captures_exact_raw_references_without_replacing_files(self):
        self.prepare()
        self.assertEqual(control.capture(self.cut_args(), self.runner), 0)
        self.assertTrue(self.runner.all_requests_committed)
        result = control.decode(control.read_private(self.case / "retained_heap_pre.json", 256 << 10))
        self.assertEqual(result["status"], "complete")
        self.assertEqual(len(result["replicas"]), 3)
        for replica in result["replicas"]:
            for key in ("session_file", "ready_file", "request_file", "started_file", "completion_file", "mount_binding_file", "mount_inspect_before_file", "mount_inspect_after_file"):
                ref = replica[key]
                self.assertEqual(control.digest(control.read_private(ref["path"], control.MOUNT_LIMIT)), ref["sha256"])
        before = len([c for c in self.runner.calls if c[0] == "kill"])
        with self.assertRaises(FileExistsError):
            control.capture(self.cut_args(), self.runner)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), before)
        self.assertEqual(control.capture(self.cut_args("post"), self.runner), 0)

    def test_failure_before_persisted_requests_sends_no_signal(self):
        self.prepare()
        (self.root / "lantern-1" / "baseline.request.json").write_text("occupied")
        self.assertEqual(control.capture(self.cut_args(), self.runner), 1)
        self.assertFalse(any(c[0] == "kill" for c in self.runner.calls))
        self.assertEqual(control.decode(control.read_private(self.case / "retained_heap_pre.json", 256 << 10))["status"], "incomplete")

    def test_uncertain_signal_does_not_become_pass_from_later_completion(self):
        self.prepare()
        self.runner.signal_error = True
        self.assertEqual(control.capture(self.cut_args(), self.runner), 1)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), 3)

    def test_incomplete_gc_and_controller_deadline_never_retry_or_pass(self):
        self.prepare()
        self.runner.damage = lambda c: c["collections"].pop()
        self.assertEqual(control.capture(self.cut_args(), self.runner), 1)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), 3)
        result = control.decode(control.read_private(self.case / "retained_heap_pre.json", 256 << 10))
        self.assertEqual(result["status"], "incomplete")

    def test_elapsed_controller_cut_is_incomplete_even_with_target_receipts(self):
        self.prepare()
        times = iter((100_000_000_000, 130_000_000_001, 130_000_000_002))
        self.assertEqual(control.capture(self.cut_args(), self.runner, clock=lambda: next(times)), 1)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), 3)

    def test_submitted_work_mismatch_refuses_before_gc(self):
        self.prepare()
        args = self.cut_args()
        args.admission = args.lookup = 999
        self.assertEqual(control.capture(args, self.runner), 1)
        self.assertFalse(any(c[0] == "kill" for c in self.runner.calls))

    def test_native_and_gc_incompleteness_refuse_without_another_signal(self):
        for damage in (lambda c: c["collections"].pop(), lambda c: c["memory"].update(cycle_after=99), lambda c: c["before"]["public"].update(active=1), lambda c: c["before"].update(receipt_entries=1), lambda c: c.update(elapsed_ns=26_000_000_000)):
            with self.subTest(damage=damage):
                request = {"schema": 1, "protocol": control.PROTOCOL, "phase": "baseline", "expected_receipts": 1000}
                value = completion(request)
                damage(value)
                with self.assertRaises(ValueError):
                    control.validate_completion(value, request, 1000)

    def test_schema_missing_duplicate_null_and_link_are_not_evidence(self):
        with self.assertRaises(ValueError):
            control.decode(b'{"schema":1,"schema":1}')
        path = self.case / "linked.json"
        target = self.case / "target.json"
        control.write_private(target, {"owned": True})
        path.symlink_to(target)
        with self.assertRaises(ValueError):
            control.read_private(path, 4096)
        request = {"phase": "baseline", "expected_receipts": 1000}
        value = completion(request)
        del value["before"]["pending_count"]
        with self.assertRaises(ValueError):
            control.validate_completion(value, request, 1000)

    def test_replacement_fifo_is_nonblocking_and_rejected_before_read(self):
        path = self.case / "replaced.json"
        control.write_private(path, {"owned": True})
        original_open = os.open
        observed = []
        def replace_before_open(candidate, flags, *args, **kwargs):
            if Path(candidate) == path:
                # Fail before the real open when NONBLOCK is missing: this
                # regression must never hang waiting for a FIFO writer.
                self.assertTrue(flags & os.O_NONBLOCK)
                observed.append(True)
                path.unlink()
                os.mkfifo(path, 0o600)
            return original_open(candidate, flags, *args, **kwargs)
        with mock.patch.object(control.os, "open", side_effect=replace_before_open):
            with self.assertRaises(ValueError):
                control.read_private(path, 4096)
        self.assertEqual(observed, [True])

    def test_opened_metadata_replacement_is_not_the_validated_regular_file(self):
        path = self.case / "replaced-mode.json"
        control.write_private(path, {"owned": True})
        original_open = os.open
        def replace_mode(candidate, flags, *args, **kwargs):
            if Path(candidate) == path:
                self.assertTrue(flags & os.O_NONBLOCK)
                path.chmod(0o644)
            return original_open(candidate, flags, *args, **kwargs)
        with mock.patch.object(control.os, "open", side_effect=replace_mode):
            with self.assertRaises(ValueError):
                control.read_private(path, 4096)

    def fresh_chain(self,mapped=False):
        parent=self.case/('mapped' if mapped else 'literal'); parent.mkdir(mode=0o700)
        root=parent/'heap-control-evidence'; control.initialize(root)
        rows=[inspect_row(root,index) for index in range(3)]
        if mapped:
            for row in rows:
                row['HostConfig']['Mounts'][-1]['Source']='/host_mnt'+row['HostConfig']['Mounts'][-1]['Source']
                row['Mounts'][-1]['Source']='/desktop'+row['Mounts'][-1]['Source']
        stage_chain(root,rows)
        return root,rows

    def test_original_first_literal_and_two_pair_proofs_are_case_local_safe_and_frozen(self):
        for mapped in (False,True):
            with self.subTest(mapped=mapped):
                root,rows=self.fresh_chain(mapped)
                runner=FakeDocker(root); runner.rows=rows
                args=copy.copy(self.args); args.directory=str(root)
                scenario=root.parent/'scenario.resolved.json'; scenario.write_bytes(self.scenario.read_bytes()); args.scenario_json=str(scenario)
                control.prepare(args,runner)
                _,config=control.load_config(root)
                for index,node in enumerate(config['nodes']):
                    binding=control.decode(control.read_private(node['mount_binding_file']['path'],control.MOUNT_LIMIT))
                    self.assertEqual(len(control.decode(control.read_private(binding['pairs_file']['path'],control.MOUNT_LIMIT))),2 if mapped else 0)
                    self.assertEqual(binding['session'],node['session'])
                    self.assertEqual(binding['probe'] is not None,mapped)
                    for key in control.CHAIN_FILES:
                        self.assertEqual(Path(binding[key]['path']).parent,root.parent/'heap-mount-evidence'/f'lantern-{index}')
                    for name in ('original-before.json','original-after.json','clone-before.json','clone-after.json'):
                        text=(root.parent/'heap-mount-evidence'/f'lantern-{index}'/name).read_text()
                        self.assertNotIn('DO_NOT_PERSIST',text); self.assertNotIn('secret-sentinel',text)
                        self.assertNotIn('Config',control.decode(text))
                    self.assertTrue((root/f'lantern-{index}'/'session.json').is_file())

    def test_missing_raw_safety_flag_is_not_projected_as_false(self):
        for group,key in (('HostConfig','Privileged'),('State','Restarting')):
            with self.subTest(group=group,key=key):
                row=inspect_row(self.root,0); del row[group][key]
                with self.assertRaises(ValueError): control.watched(row)

    def test_missing_original_proof_refuses_clone_before_any_session(self):
        parent=self.case/'unfinished'; parent.mkdir(mode=0o700)
        root=parent/'heap-control-evidence'; control.initialize(root)
        row=inspect_row(root,0)
        control.binding_command(bind_args(root,0,'bind-original-before'),row)
        control.binding_command(bind_args(root,0,'bind-original-after'),row)
        with self.assertRaises(FileNotFoundError):
            control.binding_command(bind_args(root,0,'bind-clone-before'),inspect_row(root,0,True))
        self.assertFalse(any(root.rglob('session.json')))

    def test_cross_node_daemon_mismatch_refuses_first_clone(self):
        stage=control.mount_stage(self.root,'lantern-2')
        for name in ('daemon-before','daemon-after'):
            (stage/name).write_bytes(b'different-daemon\n')
        with self.assertRaises(ValueError):
            control.binding_command(bind_args(self.root,0,'bind-clone-before'),inspect_row(self.root,0,True))

    def test_unproved_clone_source_and_nocopy_change_are_refused(self):
        for mutation in ('source','nocopy'):
            with self.subTest(mutation=mutation):
                row=inspect_row(self.root,0,True)
                if mutation=='source': row['Mounts'][-1]['Source']='/new/unproved/source'
                else: row['HostConfig']['Mounts'][0]['VolumeOptions']['NoCopy']=False
                with self.assertRaises(ValueError):
                    control.binding_command(bind_args(self.root,0,'bind-clone-before'),row)

    def test_host_directory_replacement_before_clone_or_prepare_is_not_accepted(self):
        target=self.root/'lantern-0'; target.rename(self.root/'old-lantern-0'); target.mkdir(mode=0o700)
        with self.assertRaises(ValueError):
            control.binding_command(bind_args(self.root,0,'bind-clone-before'),inspect_row(self.root,0,True))
        with self.assertRaises(ValueError): control.prepare(self.args,self.runner)
        self.assertFalse(any(self.root.rglob('session.json')))
        self.assertFalse(any(c[0] in ('kill','cp') for c in self.runner.calls))

    def test_changed_original_and_daemon_refuse_before_freezing_after(self):
        parent=self.case/'changed'; parent.mkdir(mode=0o700)
        root=parent/'heap-control-evidence'; control.initialize(root)
        row=inspect_row(root,0)
        control.binding_command(bind_args(root,0,'bind-original-before'),row)
        changed=copy.deepcopy(row); changed['HostConfig']['Mounts'][-1]['Source']='/new/source'
        with self.assertRaises(ValueError): control.binding_command(bind_args(root,0,'bind-original-after'),changed)
        args=bind_args(root,0,'bind-original-after'); args.daemon_id='other-daemon'
        with self.assertRaises(ValueError): control.binding_command(args,row)
        self.assertFalse((control.mount_stage(root,'lantern-0')/'original-after.json').exists())

    def test_bad_probe_object_profile_or_terminal_evidence_prevents_sessions(self):
        for kind in ('stat','wait','remove','command','environment','label'):
            with self.subTest(kind=kind):
                parent=self.case/('probe-'+kind); parent.mkdir(mode=0o700)
                root=parent/'heap-control-evidence'; control.initialize(root)
                row=inspect_row(root,0); row['Mounts'][-1]['Source']='/host_mnt'+row['Mounts'][-1]['Source']
                pairs=control.binding_command(bind_args(root,0,'bind-original-before'),row); write_probe(root,0,pairs)
                stage=control.mount_stage(root,'lantern-0')
                if kind in ('stat','wait','remove'):
                    name={'stat':'probe-output.log','wait':'probe-wait.log','remove':'probe-remove.log'}[kind]
                    (stage/name).write_bytes({'stat':b'1:2:directory\n1:3:directory\n','wait':b'1\n','remove':b'wrong\n'}[kind])
                else:
                    path=stage/'probe-completed.json'; value=control.decode(path.read_bytes())
                    if kind=='command': value['Config']['Cmd']=['sh','-c','true']
                    elif kind=='environment': value['Config']['Env']=['SECRET=not-allowed']
                    else: value['Config']['Labels']['lantern.owner']='foreign'
                    path.write_bytes(control.json_bytes(value))
                with self.assertRaises(ValueError): control.binding_command(bind_args(root,0,'bind-original-after'),row)
                self.assertFalse((stage/'original-after.json').exists())
                self.assertFalse(any(root.rglob('session.json')))

    def test_stat_probe_oom_fingerprint_matches_existing_boolean_domain(self):
        missing=object()
        cases=(('null-false',None,False,True),('missing-false',missing,False,True),
               ('false-null',False,None,True),('true-true',True,True,True),
               ('false-true',False,True,False),('true-false',True,False,False),
               ('false-int',False,0,False),('both-int',0,0,False))
        for name,before_oom,after_oom,accepted in cases:
            with self.subTest(name=name):
                parent=self.case/('oom-'+name); parent.mkdir(mode=0o700)
                root=parent/'heap-control-evidence'; control.initialize(root)
                row=inspect_row(root,0); row['Mounts'][-1]['Source']='/host_mnt'+row['Mounts'][-1]['Source']
                pairs=control.binding_command(bind_args(root,0,'bind-original-before'),row)
                write_probe(root,0,pairs); stage=control.mount_stage(root,'lantern-0')
                for filename,value in (('probe-created.json',before_oom),('probe-completed.json',after_oom)):
                    path=stage/filename; proof=control.decode(path.read_bytes())
                    if value is missing: proof['HostConfig'].pop('OomKillDisable',None)
                    else: proof['HostConfig']['OomKillDisable']=value
                    path.write_bytes(control.json_bytes(proof))
                raw={name:(stage/name).read_bytes() for name in control.PROBE_FILES.values()}
                probe={key:{'path':str(stage/filename),'sha256':control.digest(raw[filename])} for key,filename in control.PROBE_FILES.items()}
                if accepted:
                    control.validate_probe(probe,pairs,parent,control.watched(row))
                else:
                    with self.assertRaises(ValueError): control.validate_probe(probe,pairs,parent,control.watched(row))
                self.assertEqual(raw,{name:(stage/name).read_bytes() for name in control.PROBE_FILES.values()})
                self.assertEqual(probe,{key:{'path':str(stage/filename),'sha256':control.digest(raw[filename])} for key,filename in control.PROBE_FILES.items()})

    def test_prepare_refuses_extra_symlink_or_oversized_staging_without_signal(self):
        stage=control.mount_stage(self.root,'lantern-0')
        extra=stage/'unexpected.json'; control.write_private(extra,{'extra':True})
        with self.assertRaises(ValueError): control.prepare(self.args,self.runner)
        extra.unlink(); extra.symlink_to(stage/'host-precreate.json')
        with self.assertRaises(ValueError): control.prepare(self.args,self.runner)
        extra.unlink(); extra.write_bytes(b'x'*(control.MOUNT_LIMIT+1)); extra.chmod(0o600)
        with self.assertRaises(ValueError): control.prepare(self.args,self.runner)
        self.assertFalse(self.runner.calls)
        self.assertFalse(any(self.root.rglob('session.json')))

    def test_cut_unproved_mount_alias_cannot_signal(self):
        self.prepare()
        self.runner.inspect_damage=lambda row:row['Mounts'][-1].update(Source='/new/unproved')
        self.assertEqual(control.capture(self.cut_args(),self.runner),1)
        self.assertFalse(any(c[0]=='kill' for c in self.runner.calls))
        self.assertEqual(control.decode(control.read_private(self.case/'retained_heap_pre.json',256<<10))['status'],'incomplete')

    def test_daemon_drift_refuses_cut_before_requests_or_signal(self):
        self.prepare(); self.runner.daemon=b'different-daemon\n'
        self.assertEqual(control.capture(self.cut_args(),self.runner),1)
        self.assertFalse(any(c[0]=='kill' for c in self.runner.calls))
        self.assertFalse(any(self.root.rglob('baseline.request.json')))

    def test_mount_evidence_hardlinks_and_file_count_are_bounded(self):
        stage=control.mount_stage(self.root,'lantern-0')
        link=stage/'hardlink.json'; os.link(stage/'host-before.json',link)
        with self.assertRaises(ValueError): control.mount_directory_bounds(stage)
        link.unlink()
        for index in range(control.MOUNT_FILES):
            control.write_private(stage/f'extra-{index}.json',{'n':index})
        with self.assertRaises(ValueError): control.mount_directory_bounds(stage)

    def test_snapshots_reject_vm_pid_change_and_ref_tamper(self):
        config=self.prepare()
        first=control.inspect_nodes(config,'running',10**30,self.runner,'baseline','before')
        self.assertEqual(len(first),3)
        self.runner.inspect_damage=lambda row:row['State'].update(Pid=9999)
        with self.assertRaises(ValueError): control.inspect_nodes(config,'running',10**30,self.runner,'baseline','after')
        self.runner.inspect_damage=None
        binding=Path(config['nodes'][0]['mount_binding_file']['path'])
        binding.write_bytes(binding.read_bytes()+b' ')
        with self.assertRaises(ValueError): control.inspect_nodes(config,'running',10**30,self.runner)

    def test_tagged_recipe_has_no_new_production_surface(self):
        bench = Path(__file__).resolve().parent.parent
        recipe = (bench / "Dockerfile.retained").read_text()
        self.assertIn("FROM ${NORMAL_BUILDER_IMAGE}", recipe)
        self.assertIn("FROM ${NORMAL_RUNTIME_IMAGE}", recipe)
        self.assertIn("-tags=lantern_bench_heap", recipe)
        self.assertIn("--network=none", recipe)
        self.assertEqual(recipe.count("COPY "), 1)
        overlay = (bench / "compose.retained-heap.yml").read_text()
        self.assertEqual(overlay.count("target: /run/lantern-bench-heap"), 3)
        self.assertEqual(overlay.count("create_host_path: false"), 3)
        for surface in ("ports:", "environment:", "privileged:", "cap_add:", "network_mode:"):
            self.assertNotIn(surface, overlay)


if __name__ == "__main__":
    unittest.main()
