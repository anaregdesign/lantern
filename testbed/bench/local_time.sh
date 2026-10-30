#!/usr/bin/env bash
# Benchmark-only routing. Production keeps its fixed hostname and native client.
# No container can fall back to an upstream address on this internal network.
local_time_assert_route() {
  [[ "${LOCAL_TIME_ACTIVE:-0}" == 1 && "$LOCAL_TIME_FIXTURE_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ &&
     "$LOCAL_TIME_NETWORK_ID" =~ ^[0-9a-f]{64}$ ]] || return 1
  local a b c d octet
  IFS=. read -r a b c d <<<"$LOCAL_TIME_FIXTURE_IP"
  for octet in "$a" "$b" "$c" "$d"; do
    [[ "$octet" =~ ^(0|[1-9][0-9]{0,2})$ && "$octet" -le 255 ]] || return 1
  done
  [[ "$a" == 10 || ( "$a" == 172 && "$b" -ge 16 && "$b" -le 31 ) || ( "$a" == 192 && "$b" == 168 ) ]] || return 1
  jq -e --arg ip "$LOCAL_TIME_FIXTURE_IP" --arg network "$LOCAL_TIME_NETWORK_ID" --arg network_name "$LOCAL_TIME_NETWORK_NAME" --arg image "$LOCAL_TIME_IMAGE_ID" '
    .Image==$image and .HostConfig.ExtraHosts==[("time.asia.apple.com:"+$ip)] and
    .Config.Labels["io.lantern.bench-time.profile"]=="bench_local_udp_v1" and
    .HostConfig.NetworkMode==$network_name and
    (.NetworkSettings.Networks|keys)==[$network_name] and
    ((.State.Running|type)=="boolean") and
    (.State.Running as $running | all(.NetworkSettings.Networks[]; .NetworkID==$network or (.NetworkID=="" and $running==false))) and
    ([.Mounts[]|select(.Destination=="/etc/ntp.conf")]|length)==1 and
    all(.Mounts[]|select(.Destination=="/etc/ntp.conf"); .Type=="bind" and .RW==false)
  ' >/dev/null
}

# Full inspect input stays in a pipe; never persist serving credentials or Env.
local_time_safe_inspect() {
  local role="$1"
  jq -ce --arg role "$role" '
    {Id,Name,Image,RestartCount,
     Config:{Image:.Config.Image,User:.Config.User,Entrypoint:.Config.Entrypoint,Cmd:.Config.Cmd,
       Labels:(.Config.Labels|with_entries(select(.key=="com.docker.compose.project" or
         .key=="com.docker.compose.service" or .key=="com.docker.compose.oneoff" or .key=="lantern.owner" or .key=="io.lantern.bench-time.profile")))},
     State:(.State|{Status,Running,Pid,StartedAt,OOMKilled,ExitCode}),
     HostConfig:(.HostConfig|{NetworkMode,ExtraHosts,ReadonlyRootfs,CapDrop,SecurityOpt,Sysctls,
       CpusetCpus,NanoCpus,Memory,PidsLimit,PortBindings,Mounts,Binds,Privileged,AutoRemove,PidMode,IpcMode,UTSMode,UsernsMode,RestartPolicy,CapAdd,Devices,DeviceRequests,DeviceCgroupRules,VolumesFrom,Tmpfs,PublishAllPorts}),
     Mounts,NetworkSettings:{Networks:.NetworkSettings.Networks,Ports:.NetworkSettings.Ports}} |
    if $role=="fixture" or $role=="diagnostic" or $role=="relay" then . else
      .Mounts|=map(select(.Destination=="/etc/ntp.conf")) |
      .HostConfig.Mounts=((.HostConfig.Mounts // [])|map(select(.Target=="/etc/ntp.conf"))) |
      .HostConfig.Binds=((.HostConfig.Binds // [])|map(select(split(":")[1]=="/etc/ntp.conf")))
    end'
}

local_time_write_new() {
  local path="$1"
  (umask 077; set -o noclobber; cat > "$path")
}

local_time_record_initializer() {
  local service="$1" raw
  [[ "$service" =~ ^lantern-[012]$ ]] || return 1
  raw="$(cat)" || return 1
  local_time_assert_route <<<"$raw" || return 1
  jq -e '.State.Running==false and .State.Status=="exited" and .State.ExitCode==0 and .State.OOMKilled==false' <<<"$raw" >/dev/null || return 1
  local_time_safe_inspect initializer <<<"$raw" |
    local_time_write_new "$OUTDIR/time-source-evidence/init-${service#lantern-}.json"
}

local_time_safe_network() {
  jq -ce '{Id,Name,Driver,Internal,EnableIPv6,
    Labels:(.Labels|with_entries(select(.key=="com.docker.compose.project" or .key=="com.docker.compose.network"))),
    Containers:(.Containers|with_entries(.value|={Name,IPv4Address}))}'
}

local_time_network() {
  local network="${1:-$LOCAL_TIME_NETWORK_ID}" internal="${2:-true}" role=default
  [[ "$internal" == true ]] || role=frontend
  docker network inspect --format '{{json .}}' "$network" |
    jq -ce --arg project "$COMPOSE_PROJECT_NAME" --arg role "$role" --argjson internal "$internal" '
      select(.Driver=="bridge" and .Internal==$internal and .EnableIPv6==false and
        .Labels["com.docker.compose.project"]==$project and .Labels["com.docker.compose.network"]==$role)' |
    local_time_safe_network
}

local_time_status() {
  local container="$1" path="$2"
  [[ "$container" =~ ^[0-9a-f]{64}$ && ( "$path" == healthz || "$path" == status ) ]] || return 1
  # Literal container-loopback only. No host publisher, DNS or redirect target.
  docker exec "$container" wget -qO- -T 2 "http://127.0.0.1:8080/$path"
}

local_time_assert_fixture_only() {
  local containers networks volumes actual
  [[ "${LOCAL_TIME_ACTIVE:-0}" == 1 ]] || return 1
  containers="$(docker ps -aq --no-trunc --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME")" || return 1
  networks="$(docker network ls -q --no-trunc --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME")" || return 1
  volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME")" || return 1
  [[ "$containers" == "$LOCAL_TIME_FIXTURE_ID" && "$networks" == "$LOCAL_TIME_NETWORK_ID" && -z "$volumes" ]] || return 1
  actual="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_FIXTURE_ID")" || return 1
  local_time_validate_fixture running <<<"$actual" && local_time_network >/dev/null
}

local_time_validate_fixture() {
  local state="$1"
  jq -e --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$runtime_owner" --arg project "$COMPOSE_PROJECT_NAME" \
    --arg helper "$LOCAL_TIME_HELPER" --arg state "$state" --arg cpu "${LOCAL_TIME_CPU:-4}" '
    .Image==$image and .Config.Image==$image and .Config.User==$owner and .Config.Entrypoint==["/fixture/ntpfixture"] and
    .Config.Cmd==["-listen",":123","-status",":8080"] and .RestartCount==0 and .State.OOMKilled==false and
    (if $state=="running" then .State.Running==true and .State.Status=="running" and .State.Pid>0
     else .State.Running==false and .State.Status=="created" end) and
    .Config.Labels["com.docker.compose.project"]==$project and .Config.Labels["com.docker.compose.service"]=="time-fixture" and
    .Name==("/"+$project+"-time-fixture") and .Config.Labels["lantern.owner"]=="bench-local-udp-fixture" and
    .Config.Labels["com.docker.compose.oneoff"]=="False" and
    .Config.Labels["io.lantern.bench-time.profile"]=="bench_local_udp_v1" and
    .HostConfig.ReadonlyRootfs==true and .HostConfig.Privileged==false and .HostConfig.AutoRemove==false and
    .HostConfig.PidMode=="" and .HostConfig.IpcMode=="private" and .HostConfig.UTSMode=="" and .HostConfig.UsernsMode=="" and
    .HostConfig.CapDrop==["ALL"] and (.HostConfig.CapAdd==null or .HostConfig.CapAdd==[]) and
    .HostConfig.SecurityOpt==["no-new-privileges:true"] and
    (.HostConfig.Devices==null or .HostConfig.Devices==[]) and
    (.HostConfig.DeviceRequests==null or .HostConfig.DeviceRequests==[]) and
    (.HostConfig.DeviceCgroupRules==null or .HostConfig.DeviceCgroupRules==[]) and
    (.HostConfig.VolumesFrom==null or .HostConfig.VolumesFrom==[]) and
    (.HostConfig.Binds==null or .HostConfig.Binds==[]) and
    (.HostConfig.Tmpfs==null or .HostConfig.Tmpfs=={}) and .HostConfig.PublishAllPorts==false and
    .HostConfig.Sysctls=={"net.ipv4.ip_unprivileged_port_start":"0"} and
    .HostConfig.CpusetCpus==$cpu and .HostConfig.NanoCpus==250000000 and .HostConfig.Memory==67108864 and .HostConfig.PidsLimit==64 and
    .HostConfig.RestartPolicy=={Name:"no",MaximumRetryCount:0} and
    (.HostConfig.Mounts|length)==1 and .HostConfig.Mounts[0].Type=="bind" and
    .HostConfig.Mounts[0].Source==$helper and .HostConfig.Mounts[0].Target=="/fixture/ntpfixture" and .HostConfig.Mounts[0].ReadOnly==true and
    (.HostConfig.Mounts[0].BindOptions=={Propagation:"rprivate"} or .HostConfig.Mounts[0].BindOptions=={Propagation:"rprivate",CreateMountpoint:false}) and
    (.Mounts|length)==1 and .Mounts[0].Type=="bind" and .Mounts[0].Destination=="/fixture/ntpfixture" and .Mounts[0].RW==false and
    (.Mounts[0].Mode=="" or .Mounts[0].Mode=="ro") and .Mounts[0].Propagation=="rprivate" and
    (.NetworkSettings.Networks|length)==1 and
    (.HostConfig.PortBindings==null or .HostConfig.PortBindings=={}) and
    all((.NetworkSettings.Ports // {})[]; .==null or .==[])
  ' >/dev/null
}

local_time_prepare() {
  local architecture platform actual binding cpus service
  LOCAL_TIME_IMAGE_ID="$receipt_image_id"
  LOCAL_TIME_HELPER="$OUTDIR/time-source-evidence/ntpfixture.bin"
  LOCAL_TIME_OVERLAY="$NATIVE_WORK_DIR/local-time.json"
  [[ "$LOCAL_TIME_IMAGE_ID" =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
  cpus="$(docker info --format '{{.NCPU}}')" || return 1
  [[ "$cpus" =~ ^[1-9][0-9]*$ ]] || return 1
  LOCAL_TIME_CPU=4
  LOCAL_TIME_PLACEMENT=isolated
  if [[ "$SCENARIO_NAME" == search_qualification ]]; then
    # The short Search correctness lane retains its existing Server placement.
    # This explicitly shared fixture never qualifies the performance matrix.
    LOCAL_TIME_CPU=0
    LOCAL_TIME_PLACEMENT=shared_correctness_only
  else
    [[ "$cpus" -ge 5 && "${LANTERN_BENCH_CPUSET:-}" == 0-3 ]] || return 1
  fi
  # The formal matrix always isolates the fixture from Server CPUs 0-3.
  platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$LOCAL_TIME_IMAGE_ID")" || return 1
  case "$platform" in linux/arm64) architecture=arm64 ;; linux/amd64) architecture=amd64 ;; *) return 1 ;; esac
  mkdir -m 700 "$OUTDIR/time-source-evidence" || return 1
  mkdir -m 700 "$OUTDIR/time-source-evidence/bootstrap" || return 1
  [[ ! -e "$LOCAL_TIME_HELPER" && ! -L "$LOCAL_TIME_HELPER" ]] || return 1
  CGO_ENABLED=0 GOFLAGS= GOOS=linux GOARCH="$architecture" \
    go -C "$REPO_ROOT" build -trimpath -buildvcs=false -o "$LOCAL_TIME_HELPER" ./testbed/bench/ntpfixture || return 1
  chmod 755 "$LOCAL_TIME_HELPER" || return 1
  python3 - "$REPO_ROOT" "$OUTDIR/time-source-evidence/build.json" "$architecture" "$(command -v go)" "$PEER_TLS_DIR" <<'PY' || return 1
import hashlib,json,os,pathlib,stat,sys
repo,out,arch,tool,trust=sys.argv[1:]
tool=pathlib.Path(tool).resolve(strict=True)
expected=b'server time.asia.apple.com\n'
for i in range(3):
    path=pathlib.Path(trust,f'lantern-{i}','ntp.conf')
    before=path.lstat()
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd,'rb') as f:
        opened=os.fstat(f.fileno()); data=f.read(128); after=os.fstat(f.fileno())
    final=path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink!=1 or before.st_uid!=os.getuid() or data!=expected or any((v.st_dev,v.st_ino,v.st_size,v.st_mtime_ns)!=(before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns) for v in (opened,after,final)):
        raise SystemExit('declared native time file differs')
fd=os.open(pathlib.Path(out).parent/'ntp.conf',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
with os.fdopen(fd,'wb') as f:f.write(expected)
sources={p.relative_to(repo).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(pathlib.Path(repo,'testbed/bench/ntpfixture').glob('*.go')) if not p.name.endswith('_test.go')}
value={'sources':sources,'go_tool_sha256':hashlib.sha256(tool.read_bytes()).hexdigest(),'goos':'linux','goarch':arch,'build_flags':['-trimpath','-buildvcs=false'],'cgo_enabled':'0'}
fd=os.open(out,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
with os.fdopen(fd,'w') as f: json.dump(value,f,separators=(',',':'));f.write('\n')
PY
  go version | local_time_write_new "$OUTDIR/time-source-evidence/go-version.txt" || return 1
  # Only the fixture is created initially. Server constructors cannot precede
  # its observed IP and the final explicit /etc/hosts mapping.
  jq -n --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$runtime_owner" --arg helper "$LOCAL_TIME_HELPER" --arg cpu "$LOCAL_TIME_CPU" --arg project "$COMPOSE_PROJECT_NAME" '
    {services:{"time-fixture":{image:$image,container_name:($project+"-time-fixture"),user:$owner,read_only:true,cap_drop:["ALL"],security_opt:["no-new-privileges:true"],
      sysctls:{"net.ipv4.ip_unprivileged_port_start":"0"},cpuset:$cpu,cpus:0.25,mem_limit:"64m",pids_limit:64,
      labels:{"io.lantern.bench-time.profile":"bench_local_udp_v1","lantern.owner":"bench-local-udp-fixture"},networks:["default"],
      entrypoint:["/fixture/ntpfixture"],command:["-listen",":123","-status",":8080"],
      volumes:[{type:"bind",source:$helper,target:"/fixture/ntpfixture",read_only:true,bind:{create_host_path:false,propagation:"rprivate"}}]}},
     networks:{default:{internal:true,enable_ipv6:false}}}' | local_time_write_new "$LOCAL_TIME_OVERLAY" || return 1
  COMPOSE_FILES+=(-f "$LOCAL_TIME_OVERLAY")
  COMPOSE_STARTED=1
  docker compose -f "$LOCAL_TIME_OVERLAY" create time-fixture >/dev/null || return 1
  LOCAL_TIME_FIXTURE_ID="$(docker compose -f "$LOCAL_TIME_OVERLAY" ps --all -q time-fixture)" || return 1
  [[ "$LOCAL_TIME_FIXTURE_ID" =~ ^[0-9a-f]{64}$ ]] || return 1
  actual="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_FIXTURE_ID")" || return 1
  local_time_safe_inspect fixture <<<"$actual" | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/fixture-created.json" || return 1
  local_time_validate_fixture created <<<"$actual" || return 1
  docker compose -f "$LOCAL_TIME_OVERLAY" start time-fixture >/dev/null || return 1
  actual="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_FIXTURE_ID")" || return 1
  local_time_safe_inspect fixture <<<"$actual" | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/fixture-running.json" || return 1
  local_time_validate_fixture running <<<"$actual" || return 1
  LOCAL_TIME_NETWORK_ID="$(jq -er '.NetworkSettings.Networks|to_entries|select(length==1)|.[0].value.NetworkID' <<<"$actual")" || return 1
  LOCAL_TIME_FIXTURE_IP="$(jq -er '.NetworkSettings.Networks|to_entries|select(length==1)|.[0].value.IPAddress' <<<"$actual")" || return 1
  [[ "$LOCAL_TIME_NETWORK_ID" =~ ^[0-9a-f]{64}$ && "$LOCAL_TIME_FIXTURE_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  local a b c d octet
  IFS=. read -r a b c d <<<"$LOCAL_TIME_FIXTURE_IP"
  for octet in "$a" "$b" "$c" "$d"; do
    [[ "$octet" =~ ^(0|[1-9][0-9]{0,2})$ && "$octet" -le 255 ]] || return 1
  done
  [[ "$a" == 10 || ( "$a" == 172 && "$b" -ge 16 && "$b" -le 31 ) || ( "$a" == 192 && "$b" == 168 ) ]] || return 1
  LOCAL_TIME_NETWORK_NAME="$(docker network inspect --format '{{.Name}}' "$LOCAL_TIME_NETWORK_ID")" || return 1
  docker network inspect --format '{{json .}}' "$LOCAL_TIME_NETWORK_ID" | local_time_safe_network | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/backend.json" || return 1
  local_time_network >/dev/null || return 1
  # This fixture has no authority material; verify the actual running binary
  # content, without assuming a Desktop path alias is the same host inode.
  docker exec "$LOCAL_TIME_FIXTURE_ID" sha256sum /proc/1/exe |
    local_time_write_new "$OUTDIR/time-source-evidence/helper-executable.sha256" || return 1
  python3 - "$LOCAL_TIME_HELPER" "$OUTDIR/time-source-evidence/helper-executable.sha256" <<'PYEXEC' || return 1
import hashlib,pathlib,sys
raw=pathlib.Path(sys.argv[2]).read_text()
if raw != hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest()+"  /proc/1/exe\n": raise SystemExit("fixture executable content differs")
PYEXEC
  local ready=0
  for _ in {1..20}; do
    if local_time_status "$LOCAL_TIME_FIXTURE_ID" healthz >/dev/null; then ready=1; break; fi
    sleep 0.1
  done
  [[ "$ready" == 1 ]] || return 1
  binding="$NATIVE_WORK_DIR/local-time-clients.yml"
  # An empty list would merge with the base ports. The explicit Compose tag
  # removes every backend publisher, including protected and OFF diagnostics.
  {
    printf 'services:\n'
    for service in lantern-0 lantern-1 lantern-2; do
      printf '  %s:\n    ports: !override []\n    extra_hosts: ["time.asia.apple.com:%s"]\n    labels: {"io.lantern.bench-time.profile": "bench_local_udp_v1"}\n' "$service" "$LOCAL_TIME_FIXTURE_IP"
    done
    printf '  prometheus:\n    ports: !override []\n'
  } | local_time_write_new "$binding" || return 1
  COMPOSE_FILES+=(-f "$binding")
  LOCAL_TIME_ACTIVE=1
  local_time_assert_fixture_only
}

# No listener is allowed before the helper's read-only forwarding guard passes.
# All sockets are raw TCP to the exact private endpoints frozen below.
local_time_validate_relay() {
  local state="$1"
  jq -e --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$runtime_owner" --arg project "$COMPOSE_PROJECT_NAME" \
    --arg helper "$LOCAL_TIME_HELPER" --arg config "$LOCAL_TIME_RELAY_CONFIG" --arg state "$state" \
    --arg cpu "$LOCAL_TIME_CPU" --arg backend "$LOCAL_TIME_NETWORK_NAME" '
    .Image==$image and .Config.Image==$image and .Config.User==$owner and
    .Config.Entrypoint==["/fixture/ntpfixture"] and .Config.Cmd==["-relay-config","/fixture/relay.json"] and
    .Name==("/"+$project+"-time-relay") and .Config.Labels["com.docker.compose.project"]==$project and
    .Config.Labels["com.docker.compose.service"]=="time-relay" and .Config.Labels["com.docker.compose.oneoff"]=="False" and
    .Config.Labels["lantern.owner"]=="bench-local-tcp-relay" and .Config.Labels["io.lantern.bench-time.profile"]=="bench_local_udp_v1" and
    .RestartCount==0 and .State.OOMKilled==false and
    (if $state=="running" then .State.Running==true and .State.Status=="running" and .State.Pid>0
     else .State.Running==false and .State.Status=="created" end) and
    .HostConfig.ReadonlyRootfs==true and .HostConfig.Privileged==false and .HostConfig.AutoRemove==false and
    .HostConfig.CapDrop==["ALL"] and (.HostConfig.CapAdd==null or .HostConfig.CapAdd==[]) and
    .HostConfig.SecurityOpt==["no-new-privileges:true"] and (.HostConfig.Sysctls==null or .HostConfig.Sysctls=={}) and
    .HostConfig.PidMode=="" and .HostConfig.IpcMode=="private" and .HostConfig.UTSMode=="" and .HostConfig.UsernsMode=="" and
    (.HostConfig.Devices==null or .HostConfig.Devices==[]) and (.HostConfig.DeviceRequests==null or .HostConfig.DeviceRequests==[]) and
    (.HostConfig.DeviceCgroupRules==null or .HostConfig.DeviceCgroupRules==[]) and (.HostConfig.VolumesFrom==null or .HostConfig.VolumesFrom==[]) and
    (.HostConfig.Binds==null or .HostConfig.Binds==[]) and (.HostConfig.Tmpfs==null or .HostConfig.Tmpfs=={}) and
    .HostConfig.PublishAllPorts==false and (.HostConfig.ExtraHosts==null or .HostConfig.ExtraHosts==[]) and
    .HostConfig.RestartPolicy=={Name:"no",MaximumRetryCount:0} and
    .HostConfig.CpusetCpus==$cpu and .HostConfig.NanoCpus==1000000000 and .HostConfig.Memory==134217728 and .HostConfig.PidsLimit==128 and
    (.NetworkSettings.Networks|keys|sort)==([$backend,($project+"_frontend")]|sort) and
    (.HostConfig.NetworkMode==$backend or .HostConfig.NetworkMode==($project+"_frontend")) and
    (.HostConfig.Mounts|length)==2 and (.Mounts|length)==2 and
    ([.HostConfig.Mounts[].Target]|sort)==["/fixture/ntpfixture","/fixture/relay.json"] and
    all(.HostConfig.Mounts[]; .Type=="bind" and .ReadOnly==true and
      (if .Target=="/fixture/ntpfixture" then .Source==$helper elif .Target=="/fixture/relay.json" then .Source==$config else false end) and
      (.BindOptions=={Propagation:"rprivate"} or .BindOptions=={Propagation:"rprivate",CreateMountpoint:false})) and
    ([.Mounts[].Destination]|sort)==["/fixture/ntpfixture","/fixture/relay.json"] and
    all(.Mounts[]; .Type=="bind" and .RW==false and (.Mode=="ro" or .Mode=="") and .Propagation=="rprivate")
  ' >/dev/null
}

local_time_relay_publishers() {
  local raw
  raw="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_RELAY_ID")" || return 1
  local_time_validate_relay running <<<"$raw" || return 1
  jq -ce --argjson expected "$LOCAL_TIME_RELAY_PORTS" '
    .HostConfig.PortBindings as $requested | .NetworkSettings.Ports as $actual |
    ($expected|map({key:((.target|tostring)+"/tcp"),value:[{HostIp:.host_ip,HostPort:.published} ]})|from_entries) as $wanted |
    select($requested==$wanted and
      ([$actual|to_entries[]|select(.value!=null and .value!=[])|.key]|sort)==($wanted|keys|sort) and
      all($wanted|keys[]; . as $key|$actual[$key]==$wanted[$key])) |
    $actual' <<<"$raw"
}

local_time_start_relay() {
  local service raw container ip rows="" ports ready=0
  LOCAL_TIME_RELAY_CONFIG="$OUTDIR/time-source-evidence/relay.json"
  LOCAL_TIME_RELAY_OVERLAY="$NATIVE_WORK_DIR/local-time-relay.json"
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$service")" || return 1
    raw="$(docker inspect --format '{{json .}}' "$container")" || return 1
    local_time_safe_inspect serving <<<"$raw" | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/$service.json" || return 1
    local_time_assert_route <<<"$raw" || return 1
    jq -e '.State.Running==true and (.HostConfig.PortBindings==null or .HostConfig.PortBindings=={}) and all((.NetworkSettings.Ports // {})[]; .==null or .==[])' <<<"$raw" >/dev/null || return 1
    ip="$(jq -er --arg network "$LOCAL_TIME_NETWORK_NAME" '.NetworkSettings.Networks[$network].IPAddress' <<<"$raw")" || return 1
    rows+="$(jq -nc --arg service "$service" --arg ip "$ip" '{service:$service,ip:$ip}')"$'\n'
  done
  if [[ "$receipt_driver" == 0 ]]; then
    container="$(docker compose "${COMPOSE_FILES[@]}" ps -q prometheus)" || return 1
    raw="$(docker inspect --format '{{json .}}' "$container")" || return 1
    local_time_safe_inspect diagnostic <<<"$raw" | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/prometheus.json" || return 1
    jq -e --arg network "$LOCAL_TIME_NETWORK_NAME" --arg id "$LOCAL_TIME_NETWORK_ID" --arg project "$COMPOSE_PROJECT_NAME" '
      .Config.Labels["com.docker.compose.project"]==$project and .Config.Labels["com.docker.compose.service"]=="prometheus" and .State.Running==true and
      (.NetworkSettings.Networks|keys)==[$network] and .NetworkSettings.Networks[$network].NetworkID==$id and
      (.HostConfig.PortBindings==null or .HostConfig.PortBindings=={}) and all((.NetworkSettings.Ports // {})[]; .==null or .==[])' <<<"$raw" >/dev/null || return 1
    ip="$(jq -er --arg network "$LOCAL_TIME_NETWORK_NAME" '.NetworkSettings.Networks[$network].IPAddress' <<<"$raw")" || return 1
    rows+="$(jq -nc --arg ip "$ip" '{service:"prometheus",ip:$ip}')"$'\n'
  fi
  # Only the safe backend address projections are inputs to this fixed recipe.
  printf '%s' "$rows" | python3 -c '
import ipaddress,json,os,sys
path,receipt=sys.argv[1:]; nodes=[json.loads(v) for v in sys.stdin if v.strip()]
expected=["lantern-0","lantern-1","lantern-2"]+([] if receipt=="1" else ["prometheus"])
if [v["service"] for v in nodes]!=expected: raise SystemExit("relay backend cohort differs")
for node in nodes:
    ip=ipaddress.IPv4Address(node["ip"])
    if str(ip)!=node["ip"] or not any(ip in ipaddress.IPv4Network(n) for n in ("10.0.0.0/8","172.16.0.0/12","192.168.0.0/16")): raise SystemExit("relay backend is not private IPv4")
if len({v["ip"] for v in nodes})!=len(nodes):raise SystemExit("duplicate relay backend")
routes=[{"listen_port":6380+i,"backend_ip":nodes[i]["ip"],"backend_port":6380} for i in range(3)]
if receipt=="0":routes += [{"listen_port":9090+i,"backend_ip":nodes[i]["ip"],"backend_port":9090} for i in range(3)]+[{"listen_port":9093,"backend_ip":nodes[3]["ip"],"backend_port":9090}]
value={"schema":1,"protocol":"bench_local_tcp_relay_v1","max_connections":4096,"frontend_ipv6":False,"backend_ipv6":False,"routes":routes}
fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
with os.fdopen(fd,"w") as f:json.dump(value,f,separators=(",",":"));f.write("\n")
' "$LOCAL_TIME_RELAY_CONFIG" "$receipt_driver" || return 1
  ports="$(jq -nc --argjson api "$(printf '%s\n' "${REPLICA_GRPC_PORTS[@]}"|jq -sc 'map(tonumber)')" \
    --argjson metrics "$(printf '%s\n' "${REPLICA_METRICS_PORTS[@]}"|jq -sc 'map(tonumber)')" \
    --arg prom "${LANTERN_BENCH_PROM_PORT:-9091}" --arg receipt "$receipt_driver" '
      [range(0;3)|{target:(6380+.),published:($api[.]|tostring),host_ip:"127.0.0.1",protocol:"tcp"}]+
      (if $receipt=="1" then [] else [range(0;3)|{target:(9090+.),published:($metrics[.]|tostring),host_ip:"127.0.0.1",protocol:"tcp"}]+
        [{target:9093,published:$prom,host_ip:"127.0.0.1",protocol:"tcp"}] end)')" || return 1
  LOCAL_TIME_RELAY_PORTS="$ports"
  jq -n --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$runtime_owner" --arg helper "$LOCAL_TIME_HELPER" \
    --arg config "$LOCAL_TIME_RELAY_CONFIG" --arg cpu "$LOCAL_TIME_CPU" --arg project "$COMPOSE_PROJECT_NAME" --argjson ports "$ports" '
    {services:{"time-relay":{image:$image,container_name:($project+"-time-relay"),user:$owner,read_only:true,
      cap_drop:["ALL"],security_opt:["no-new-privileges:true"],cpuset:$cpu,cpus:1,mem_limit:"128m",pids_limit:128,
      labels:{"io.lantern.bench-time.profile":"bench_local_udp_v1","lantern.owner":"bench-local-tcp-relay"},networks:["default","frontend"],
      entrypoint:["/fixture/ntpfixture"],command:["-relay-config","/fixture/relay.json"],ports:$ports,
      volumes:[{type:"bind",source:$helper,target:"/fixture/ntpfixture",read_only:true,bind:{create_host_path:false,propagation:"rprivate"}},
        {type:"bind",source:$config,target:"/fixture/relay.json",read_only:true,bind:{create_host_path:false,propagation:"rprivate"}}]}},
     networks:{default:{internal:true,enable_ipv6:false},frontend:{internal:false,enable_ipv6:false}}}' |
    local_time_write_new "$LOCAL_TIME_RELAY_OVERLAY" || return 1
  # Frontend is introduced only now, after the original-first initializer guard.
  COMPOSE_FILES+=(-f "$LOCAL_TIME_RELAY_OVERLAY")
  LOCAL_TIME_RELAY_DECLARED=1
  docker compose -f "$LOCAL_TIME_RELAY_OVERLAY" create time-relay >/dev/null || return 1
  LOCAL_TIME_RELAY_ID="$(docker compose -f "$LOCAL_TIME_RELAY_OVERLAY" ps --all -q time-relay)" || return 1
  [[ "$LOCAL_TIME_RELAY_ID" =~ ^[0-9a-f]{64}$ ]] || return 1
  raw="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_RELAY_ID")" || return 1
  local_time_safe_inspect relay <<<"$raw" | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/relay-created.json" || return 1
  local frontend
  frontend="$(docker network inspect --format '{{json .}}' "${COMPOSE_PROJECT_NAME}_frontend")" || return 1
  printf '%s\n' "$frontend" | local_time_safe_network | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/frontend-created.json" || return 1
  LOCAL_TIME_FRONTEND_ID="$(jq -er '.Id' <<<"$frontend")" || return 1
  [[ "$LOCAL_TIME_FRONTEND_ID" =~ ^[0-9a-f]{64}$ ]] || return 1
  local_time_network "$LOCAL_TIME_FRONTEND_ID" false >/dev/null || return 1
  local_time_network >/dev/null || return 1
  local_time_validate_relay created <<<"$raw" || return 1
  docker compose -f "$LOCAL_TIME_RELAY_OVERLAY" start time-relay >/dev/null || return 1
  raw="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_RELAY_ID")" || return 1
  local_time_safe_inspect relay <<<"$raw" | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/relay-running.json" || return 1
  LOCAL_TIME_FRONTEND_ID="$(jq -er --arg name "${COMPOSE_PROJECT_NAME}_frontend" '.NetworkSettings.Networks[$name].NetworkID' <<<"$raw")" || return 1
  [[ "$LOCAL_TIME_FRONTEND_ID" =~ ^[0-9a-f]{64}$ ]] || return 1
  docker network inspect --format '{{json .}}' "$LOCAL_TIME_FRONTEND_ID" | local_time_safe_network |
    local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/frontend.json" || return 1
  local_time_validate_relay running <<<"$raw" || return 1
  local_time_network "$LOCAL_TIME_FRONTEND_ID" false >/dev/null || return 1
  local_time_relay_publishers | local_time_write_new "$OUTDIR/time-source-evidence/bootstrap/publishers.json" || return 1
  docker exec "$LOCAL_TIME_RELAY_ID" sha256sum /proc/1/exe |
    local_time_write_new "$OUTDIR/time-source-evidence/relay-executable.sha256" || return 1
  [[ "$(cat "$OUTDIR/time-source-evidence/helper-executable.sha256")" == "$(cat "$OUTDIR/time-source-evidence/relay-executable.sha256")" ]] || return 1
  for _ in {1..20}; do
    if local_time_status "$LOCAL_TIME_RELAY_ID" healthz >/dev/null; then ready=1; break; fi
    sleep 0.1
  done
  [[ "$ready" == 1 ]] || return 1
}

local_time_capture() {
  local phase="$1" directory="$OUTDIR/time-source-evidence/$1" service container raw
  [[ "$phase" == pre || "$phase" == post ]] || return 1
  mkdir -m 700 "$directory" || return 1
  raw="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_FIXTURE_ID")" || return 1
  local_time_validate_fixture running <<<"$raw" || return 1
  local_time_safe_inspect fixture <<<"$raw" | local_time_write_new "$directory/fixture.json" || return 1
  local_time_network | local_time_write_new "$directory/network.json" || return 1
  local_time_status "$LOCAL_TIME_FIXTURE_ID" status |
    local_time_write_new "$directory/status.json" || return 1
  raw="$(docker inspect --format '{{json .}}' "$LOCAL_TIME_RELAY_ID")" || return 1
  local_time_safe_inspect relay <<<"$raw" | local_time_write_new "$directory/relay.json" || return 1
  local_time_validate_relay running <<<"$raw" || return 1
  local_time_relay_publishers >/dev/null || return 1
  local_time_network "$LOCAL_TIME_FRONTEND_ID" false | local_time_write_new "$directory/frontend.json" || return 1
  local_time_status "$LOCAL_TIME_RELAY_ID" status | local_time_write_new "$directory/relay-status.json" || return 1
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$service")" || return 1
    raw="$(docker inspect --format '{{json .}}' "$container")" || return 1
    local_time_assert_route <<<"$raw" || return 1
    local_time_safe_inspect serving <<<"$raw" | local_time_write_new "$directory/serving-${service#lantern-}.json" || return 1
  done
  if [[ "$receipt_driver" == 0 ]]; then
    container="$(docker compose "${COMPOSE_FILES[@]}" ps -q prometheus)" || return 1
    raw="$(docker inspect --format '{{json .}}' "$container")" || return 1
    jq -e --arg project "$COMPOSE_PROJECT_NAME" '.Config.Labels["com.docker.compose.project"]==$project and .Config.Labels["com.docker.compose.service"]=="prometheus" and .State.Running==true' <<<"$raw" >/dev/null || return 1
    local_time_safe_inspect diagnostic <<<"$raw" | local_time_write_new "$directory/diagnostic-prometheus.json" || return 1
  fi
  python3 - "$OUTDIR" "$phase" "$receipt_image_commit" "${EXPECTED_LANTERN_TREE:-}" "$SCENARIO_NAME" "$COMPOSE_PROJECT_NAME" "$retained_heap" "$LOCAL_TIME_IMAGE_ID" "$runtime_owner" "${LANTERN_BENCH_CPUSET:-}" "$receipt_driver" "$LOCAL_TIME_PLACEMENT" "$PEER_TLS_DIR" <<'PY'
import hashlib,json,os,pathlib,stat,sys
out,phase,commit,tree,scenario,run,retained,image,user,cpuset,receipt,placement,trust=sys.argv[1:]
root=pathlib.Path(out).resolve(strict=True); evidence=root/'time-source-evidence'; cut=evidence/phase
def ref(path): return {'path':str(path),'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
build=json.loads((evidence/'build.json').read_text()); build['go_version']=(evidence/'go-version.txt').read_text().split()[2]
helper={**ref(evidence/'ntpfixture.bin'),**build}
source_configs=[]
for i in range(3):
    path=pathlib.Path(trust,f'lantern-{i}','ntp.conf')
    before=path.lstat()
    if path.resolve(strict=True)!=path: raise SystemExit('physical declared time source required')
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd,'rb') as f:
        opened=os.fstat(f.fileno()); data=f.read(128); after=os.fstat(f.fileno())
    final=path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink!=1 or before.st_uid!=os.getuid() or data!=b'server time.asia.apple.com\n' or any((v.st_dev,v.st_ino,v.st_size,v.st_mtime_ns)!=(before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns) for v in (opened,after,final)):
        raise SystemExit('declared native time file differs')
    source_configs.append({'service':f'lantern-{i}','path':str(path),'sha256':hashlib.sha256(data).hexdigest()})
value={'schema':1,'profile':'bench_local_udp_v1','phase':phase,'source_commit':commit,'source_tree':tree,'scenario':scenario,'run_id':root.name,'scope':'retained' if retained=='1' else 'normal','image_id':image,'runtime_user':user,'server_cpuset':cpuset,'fixture_placement':placement,'helper':helper,'helper_executable':ref(evidence/'helper-executable.sha256'),'ntp_config':ref(evidence/'ntp.conf'),'source_configs':source_configs,'transport_profile':'bench_local_tcp_relay_v1',
       'relay':ref(cut/'relay.json'),'frontend':ref(cut/'frontend.json'),'relay_status':ref(cut/'relay-status.json'),'relay_config':ref(evidence/'relay.json'),'relay_executable':ref(evidence/'relay-executable.sha256'),
       'fixture':ref(cut/'fixture.json'),'network':ref(cut/'network.json'),'status':ref(cut/'status.json'),'serving':[ref(cut/f'serving-{i}.json') for i in range(3)],'initializers':[ref(evidence/f'init-{i}.json') for i in range(3)] if receipt=='1' else [],'diagnostics':[] if receipt=='1' else [ref(cut/'diagnostic-prometheus.json')]}
fd=os.open(root/f'time_source_{phase}.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
with os.fdopen(fd,'w') as f: json.dump(value,f,separators=(',',':'));f.write('\n')
PY
}

# Retained failures may keep volumes for inspection, but never leave a time
# source running after its clients have stopped. Unknown identities refuse.
local_time_stop() {
  local service container raw
  local -a containers=()
  for service in lantern-0 lantern-1 lantern-2 time-relay time-fixture; do
    if [[ "$service" == time-relay && "${LOCAL_TIME_RELAY_DECLARED:-0}" != 1 ]]; then continue; fi
    container="$(docker compose "${COMPOSE_FILES[@]}" ps --all -q "$service")" || return 1
    [[ -n "$container" ]] || continue
    [[ "$container" =~ ^[0-9a-f]{64}$ ]] || return 1
    raw="$(docker inspect --format '{{json .}}' "$container")" || return 1
    jq -e --arg id "$container" --arg project "$COMPOSE_PROJECT_NAME" --arg service "$service" --arg image "$LOCAL_TIME_IMAGE_ID" --arg owner "$runtime_owner" '
      .Id==$id and .Config.Labels["com.docker.compose.project"]==$project and .Config.Labels["com.docker.compose.service"]==$service and
      .Config.Labels["io.lantern.bench-time.profile"]=="bench_local_udp_v1" and .Image==$image and .Config.User==$owner' <<<"$raw" >/dev/null || return 1
    if [[ "$service" == time-fixture ]]; then [[ "$container" == "$LOCAL_TIME_FIXTURE_ID" ]] || return 1; fi
    if [[ "$service" == time-relay ]]; then [[ "$container" == "${LOCAL_TIME_RELAY_ID:-}" ]] || return 1; fi
    containers+=("$container")
  done
  # Exact IDs in client-first order; names are never resolved again to signal.
  for container in "${containers[@]}"; do
    if [[ "$container" == "$LOCAL_TIME_FIXTURE_ID" || "$container" == "${LOCAL_TIME_RELAY_ID:-}" ]]; then
      docker stop --time 10 "$container" >/dev/null || return 1
    else
      docker stop --time 45 "$container" >/dev/null || return 1
    fi
    docker inspect --format '{{json .}}' "$container" | jq -e --arg id "$container" '
      .Id==$id and .State.Running==false and (.State.Status=="exited" or .State.Status=="created")' >/dev/null || return 1
  done
}
