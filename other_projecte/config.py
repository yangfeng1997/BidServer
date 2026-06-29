#!/usr/bin/env python3

import argparse
import fcntl, fnmatch
import getpass
import os
import re
import socket, struct, shutil
import yaml

# ------------------------------------------------------------------------------
# global variables
ENV_POOL = {
    # 测试环境
    "user00@10.130.85.153" : (49, "DailyEnv"),
    "user00@10.130.84.74"  : (50, "StableEnv")
}

DEFAULT_GATESVR_TCP_PORT = 9999
DEFAULT_GATESVR_UDP_PORT = 9992
DEFAULT_EXTERNAL_GATESVR_PORT = 9999

# 配置文件路径
CFG_PATH = "cfg"
RUN_PATH = "run"
CFG_HOME_PATH = os.path.join(CFG_PATH, "home")
HOME_YAML_PATH = os.path.join(RUN_PATH, "home.yaml")
COMM_TMPL_PATH = os.path.join(CFG_PATH, "commconf", "comm.yaml_template")
COMM_YAML_PATH = os.path.join(RUN_PATH, "commconf", "comm.yaml")
DEBUG_SVRS_OVERLAY_PATH = os.path.join(RUN_PATH, "debug_svrs.yaml")

#udp ip port info
GATESVR_EXTERNAL_IP = None
GATESVR_EXTERNAL_UDP_PORT = None
GATESVR_EXTERNAL_TCP_PORT = None
DEFAULT_LOGINSVR_COUNT = 1
LOGINSVR_COUNT = None
# ------------------------------------------------------------------------------
# tool class
class CustomDumper(yaml.Dumper):
    def increase_indent(self, flow=False, indentless=False):
        return super(CustomDumper, self).increase_indent(flow, False)
    
def safe_remove_file(path):
    if os.path.exists(path):
        os.remove(path)

# ------------------------------------------------------------------------------
# configure process functions
def init_environment():
    # check if run directory exist
    if not os.path.exists(RUN_PATH):
        print(f"Run path not found: {RUN_PATH}, create it now")
        os.makedirs(RUN_PATH)

    # check if home template exist
    if not os.path.exists(HOME_TMPL_PATH):
        print(f"Home template not found: {HOME_TMPL_PATH}")
        return -1

    return 0

def init_clientversion(version):
    versiondata = {
        "main_version": -1,
        "sub_version": -1,
        "program_version": -1,
        "resource_version": -1
    }
    if version is None:
        return versiondata
    parts = version.split(".")
    if len(parts) != 4:
        return versiondata
    versiondata["main_version"] = int(parts[0])
    versiondata["sub_version"] = int(parts[1])
    versiondata["program_version"] = int(parts[2])
    versiondata["resource_version"] = int(parts[3])
    return versiondata

def generate_home_yaml(world_id, gatesvr_tcp_port, gatesvr_udp_port, public_world):
    # load home template data
    data = yaml_load(HOME_TMPL_PATH)

    # replace args
    data['world_id'] = world_id
    data['gatesvr_tcp_port'] = gatesvr_tcp_port
    data['gatesvr_udp_port'] = gatesvr_udp_port
    data['public_world'] = public_world
    data['env'] = ENV
    
    external_ip, external_udp_port, external_tcp_port = get_gatesvr_external_info_by_world_id(data)
    if external_ip == "" or external_udp_port == 0:
        print(f"UDP连接信息错误：IP地址或端口无效。IP: {external_ip}, Port: {external_udp_port}")
        exit(-1)
    
    data['gatesvr_external_tcp_ip'] = external_ip
    data['gatesvr_external_udp_ip'] = external_ip
    data['gatesvr_external_tcp_port'] = external_tcp_port
    data['gatesvr_external_udp_port'] = external_udp_port


    data['loginsvr_count'] = LOGINSVR_COUNT

    data.setdefault('region', '')

    sd_agent = data.setdefault('service_discovery', {}).setdefault('agent', {})
    sd_agent.setdefault('socket_path', '/tmp/silver-sdagent.sock')
    sd_agent.setdefault('etcd_endpoints', ['https://devetcd.silver.he:443'])
    sd_agent.setdefault('etcd_tls', {'enabled': True, 'insecure_skip_verify': True})
    sd_agent.setdefault('etcd_auto_sync_interval_seconds', 0)

    # Merge optional debug overlay for local public-svr debugging. The overlay
    # file lives under run/ (gitignored) so each developer can toggle extra
    # svrs without polluting the shared template / deployment pipeline.
    apply_debug_svrs_overlay(data)

    # save home yaml
    yaml_save(HOME_YAML_PATH, data)

    return data

def apply_debug_svrs_overlay(data):
    if not os.path.exists(DEBUG_SVRS_OVERLAY_PATH):
        return

    overlay = yaml_load(DEBUG_SVRS_OVERLAY_PATH)
    if not overlay:
        return
    if not isinstance(overlay, list):
        print(f">>> {DEBUG_SVRS_OVERLAY_PATH}: expected a yaml list of svr names, got {type(overlay).__name__}; ignored")
        return

    svr_list = data.get('svr_list', {}) or {}
    extras = []
    for name in overlay:
        if not isinstance(name, str):
            print(f">>> {DEBUG_SVRS_OVERLAY_PATH}: skipping non-string entry {name!r}")
            continue
        if name not in svr_list:
            print(f">>> {DEBUG_SVRS_OVERLAY_PATH}: skipping {name!r} (not in svr_list)")
            continue
        extras.append(name)

    if not extras:
        return

    # Append to start_svrs / stop_svrs; dedup while preserving order.
    for key in ('start_svrs', 'stop_svrs'):
        base = data.get(key) or []
        seen = set(base)
        merged = list(base)
        for name in extras:
            if name not in seen:
                merged.append(name)
                seen.add(name)
        data[key] = merged

    print(f">>> debug_svrs overlay applied: {extras}")


def get_gatesvr_external_info_by_world_id(data):
    if GATESVR_EXTERNAL_IP != None and GATESVR_EXTERNAL_UDP_PORT != None and GATESVR_EXTERNAL_TCP_PORT != None:
        return GATESVR_EXTERNAL_IP, GATESVR_EXTERNAL_UDP_PORT, GATESVR_EXTERNAL_TCP_PORT
    world_id = data['world_id']
    mongodata = data['db']
    formatted_uri = "{uri}".format(uri=mongodata['uri'])
    try:
        import pymongo

        myclient = pymongo.MongoClient(formatted_uri)

        if "GlobalData" not in myclient.list_database_names():
            raise BaseException("GlobalData is not in mongo db list")
        
        mydb = myclient["GlobalData"]
        if "Servers" not in mydb.list_collection_names():
            raise BaseException("Servers is not in mongo collection list of GlobalData")
        
        mycol = mydb["Servers"]
        x = mycol.find_one({"world_id": world_id})
        if x is None:
            raise BaseException("No world id {wid} found in db, use local value".format(wid=world_id))
        
        print(x)
        
        if "udp_port" not in x["addr"][0]:
            # 未设置udp_port值的服务器使用world_id*100+92暂行计算
            print("Server {sid} udp port not set in mongo, use 100*world_id+92. ip {ip}, port {port}".format(sid=world_id, ip=x["addr"][0]["ip"], port=world_id*100+92))
            return x["addr"][0]["ip"], world_id*100+92, world_id*100+99
        else:
            print("Server {sid} udp info found in mongo, use it. ip {ip}, port {port}".format(sid=world_id, ip=x["addr"][0]["ip"], port=x["addr"][0]["udp_port"]))
            return x["addr"][0]["ip"], x["addr"][0]["udp_port"] , x["addr"][0]["port"]
    except BaseException as e:
        print("get udp port by mongo error, no cfg pointed ,plz add to mongo server GlobalData.Servers:" + str(e))
        return "", 0  , 0 

def generate_directory_structure(home_data):
    # generate commconf directory
    base_dir = os.path.join(RUN_PATH, "commconf")
    if not os.path.exists(base_dir):
        # create the base directory
        os.makedirs(base_dir)
        print(f"Created base directory: {base_dir}")

    # generate designerconf directory
    conf_dir = os.path.join(RUN_PATH, "design","designerconf")
    if not os.path.exists(conf_dir):
        # create the base directory
        os.makedirs(conf_dir)
        print(f"Created conf directory: {conf_dir}")

    ready_dir = os.path.join(RUN_PATH, "preapplicationready")
    if not os.path.exists(ready_dir):
        # create the base directory
        os.makedirs(ready_dir)
        print(f"Created conf directory: {ready_dir}")

    # check 'svr_list' in home data
    if not 'svr_list' in home_data:
        return -1

    # define the subdirectories to be created
    subdirectories = ['bin', 'conf', 'log','monitor']

    for key in home_data['svr_list'].keys():
        # check if the base directory exists
        base_dir = os.path.join(RUN_PATH, key)
        if not os.path.exists(base_dir):
            # create the base directory
            os.makedirs(base_dir)
            print(f"Created base directory: {base_dir}")

        # create each subdirectory
        for subdir in subdirectories:
            subdir_path = os.path.join(base_dir, subdir)
            if not os.path.exists(subdir_path):
                os.makedirs(subdir_path)
                print(f"Created subdirectory: {subdir_path}")
    return 0

def copy_files():
    # copy scripts
    shutil.copy2('cfg/startall.sh','run/startall.sh')
    shutil.copy2('cfg/stopall.sh','run/stopall.sh')
    shutil.copy2('cfg/clearall.sh','run/clearall.sh')
    shutil.copy2('cfg/clearall_violence.sh','run/clearall_violence.sh')
    shutil.copy2('cfg/restartall.sh','run/restartall.sh')
    shutil.copy2('cfg/checkall.sh','run/checkall.sh')
    shutil.copy2('cfg/killall.sh','run/killall.sh')
    shutil.copy2('cfg/cleanallshm.sh','run/cleanallshm.sh')
    shutil.copy2('cfg/clearlogs.sh','run/clearlogs.sh')
    shutil.copy2('cfg/get_designer_config.sh','run/get_designer_config.sh')
    shutil.copy2('cfg/get_designer_config_private.sh','run/get_designer_config_private.sh')
    shutil.copy2('cfg/switchCoreMonitor.sh','run/switchCoreMonitor.sh')
    shutil.copy2('cfg/reloadResAll.sh','run/reloadResAll.sh')

    os.chmod("run/startall.sh", 0o755)
    os.chmod("run/stopall.sh", 0o755)
    os.chmod("run/clearall.sh", 0o755)
    os.chmod("run/clearall_violence.sh", 0o755)
    os.chmod("run/restartall.sh", 0o755)
    os.chmod("run/killall.sh", 0o755)
    os.chmod("run/switchCoreMonitor.sh", 0o755)
    os.chmod("run/get_designer_config.sh", 0o755)
    os.chmod("run/get_designer_config_private.sh", 0o755)

    # copy secret keys
    shutil.copytree('cfg/secret_key', 'run/secret_key', dirs_exist_ok=True)

    return 0

def generate_commconf_files(home_data):
    # load template data
    tmpl_data = yaml_load(COMM_TMPL_PATH)

    # replace by home data
    data = yaml_replace_placeholders(tmpl_data, home_data)
    if data is None:
        print(f">>> {COMM_TMPL_PATH} generate failed")
        return -1

    # save yaml
    yaml_save(COMM_YAML_PATH, data)

    return 0

def generate_server_files(home_data):
    # check 'svr_list' in home data
    if not 'svr_list' in home_data:
        return 0

    #################### begin to generate ####################
    for key in home_data['svr_list'].keys():
        # load template data
        tmpl_path = os.path.join(CFG_PATH, key)
        yaml_path = os.path.join(RUN_PATH, key, "conf")

        # make sure directory exist
        os.makedirs(yaml_path, exist_ok=True)

        # generate every yaml
        for root, dirs, files in os.walk(tmpl_path):
            for filename in fnmatch.filter(files, '*.yaml_template'):
                # calc yaml file path
                yaml_filename = filename.replace('.yaml_template', '.yaml')
                yaml_filepath = os.path.join(yaml_path, yaml_filename)

                # load template data
                tmpl_data = yaml_load(os.path.join(tmpl_path, filename))
                if tmpl_data is None:
                    with open(yaml_filepath, "w") as file:
                        pass
                    continue

                # replace by home data
                data = yaml_replace_placeholders(tmpl_data, home_data)
                if data is None:
                    print(f">>> Yaml {yaml_filepath} generate failed")
                    return -1

                # save yaml
                yaml_save(yaml_filepath, data)

        # generate scirpts
        world_id = home_data['world_id']
        svr_type = home_data['svr_list'][key]['id']
        shm_size = 0
        if ('shm_size' in home_data['svr_list'][key]):
            shm_size = home_data['svr_list'][key]['shm_size']

        ret = create_server_script(key, svr_type, world_id, shm_size)
        if 0 != ret:
            print(f">>> {key} scripts generate failed")
            return -2

        formatted_key = "{:<20}".format(key)
        print(f">>> {formatted_key} files generate success")

    return 0

def create_server_script(svr_name, svr_type, world_id, shm_size):
    base_dir = os.path.join(RUN_PATH, svr_name, 'bin')

    svr_addr = str(world_id) + ".0." + str(svr_type) + ".0"
    shm_key = (world_id << 21) | (svr_type << 10) | 0x0

    def write_script(file_path, content):
        with open(file_path, 'w') as file:
            file.write(content)
        os.chmod(file_path, 0o755)

    # start args
    start_args = f"--addr {svr_addr} "
    start_args += f"--pid-file {svr_name}.pid "
    start_args += f"--log-file ../conf/{svr_name}_hlog.yaml "
    start_args += f"--conf-file ../conf/{svr_name}.yaml{' --shm-size ' + str(shm_size) if shm_size > 0 else ''} "

    # Generate start script
    start_script =  f"#!/bin/sh\n"
    start_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
    start_script += f"cd $DIR\n"
    start_script += f"./{svr_name} {start_args} --daemon start >>../log/{svr_name}.stdout.log 2>>../log/{svr_name}.stderr.log\n"
    write_script(os.path.join(base_dir, 'start.sh'), start_script)

    # Generate stop script
    stop_script =  f"#!/bin/sh\n"
    stop_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
    stop_script += f"cd $DIR\n"
    stop_script += f"lastPid=$(cat {svr_name}.pid)\n"
    stop_script += f"./{svr_name} -p {svr_name}.pid stop\n"
    stop_script += f"if [ $? -ne 0 ]; then\n"
    stop_script += f"\texit $?\n"
    stop_script += f"fi\n"
    stop_script += f"echo $lastPid > {svr_name}_last_stop.pid\n"
    write_script(os.path.join(base_dir, 'stop.sh'), stop_script)
    
    # Generate clear script
    clear_script =  f"#!/bin/sh\n"
    clear_script += f"ipcrm -M {shm_key} > /dev/null 2>&1\n"
    clear_script += f"echo \">>> clear {svr_name} shm ({shm_key}) success.\""
    if shm_size > 0:
        write_script(os.path.join(base_dir, 'clear.sh'), clear_script)

    # Generate gdb start script
    gdb_start_script =  f"#!/bin/sh\n"
    gdb_start_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
    gdb_start_script += f"cd $DIR\n"
    gdb_start_script += f"gdb --args ./{svr_name} {start_args} start\n"
    write_script(os.path.join(base_dir, 'gdb_start.sh'), gdb_start_script)

    # Generate gdb attach script
    gdb_attach_script =  f"#!/bin/sh\n"
    gdb_attach_script += f"gdb -p `cat {svr_name}.pid`"
    write_script(os.path.join(base_dir, 'gdb_attach.sh'), gdb_attach_script)

    # Generate pydbg attach script (scenesvr only — 其他服务不内嵌 IPython kernel)
    if svr_name == "scenesvr":
        pydbg_attach_script  = f"#!/bin/sh\n"
        pydbg_attach_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
        pydbg_attach_script += f"CONN_FILE=\"$DIR/kernel-scenesvr.json\"\n"
        pydbg_attach_script += f"if [ ! -f \"$CONN_FILE\" ]; then\n"
        pydbg_attach_script += f"\techo \"[pydbg_attach] 连接文件不存在: $CONN_FILE\" >&2\n"
        pydbg_attach_script += f"\techo \"[pydbg_attach] 检查: 1) scenesvr 是否在跑; 2) 是否用 ENABLE_PYDBG=ON (./build.sh -p) 构建\" >&2\n"
        pydbg_attach_script += f"\texit 1\n"
        pydbg_attach_script += f"fi\n"
        pydbg_attach_script += f"if ! command -v jupyter >/dev/null 2>&1; then\n"
        pydbg_attach_script += f"\techo \"[pydbg_attach] 未找到 jupyter: pip install jupyter_console ipykernel\" >&2\n"
        pydbg_attach_script += f"\texit 1\n"
        pydbg_attach_script += f"fi\n"
        pydbg_attach_script += f"exec jupyter console --existing \"$CONN_FILE\"\n"
        write_script(os.path.join(base_dir, 'pydbg_attach.sh'), pydbg_attach_script)

    # Generate reload script
    reload_script =  f"#!/bin/sh\n"
    reload_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
    reload_script += f"cd $DIR\n"
    reload_script += f"if [ ! -f {svr_name}.pid ] || [ ! -s {svr_name}.pid ]; then\n"
    reload_script += f"\techo \"{svr_name}.pid not found or empty, skip reload.\"\n"
    reload_script += f"\texit 0\n"
    reload_script += f"fi\n"
    reload_script += f"lastPid=$(cat {svr_name}.pid)\n"
    reload_script += f"if [ -z \"$lastPid\" ]; then\n"
    reload_script += f"\techo \"No PID found in {svr_name}.pid, skip reload.\"\n"
    reload_script += f"\texit 0\n"
    reload_script += f"fi\n"
    reload_script += f"./{svr_name} -p {svr_name}.pid reload\n"
    reload_script += f"if [ $? -ne 0 ]; then\n"
    reload_script += f"\texit $?\n"
    reload_script += f"fi\n"
    reload_script += f'echo "$(date \'+%F %T\')" >> {svr_name}_reload.pid\n'
    write_script(os.path.join(base_dir, 'reload.sh'), reload_script)

    return 0

SD_AGENT_NAME = 'sdagent'

def generate_sdagent_files(home_data):
    # sdagent is NOT in svr_list — it's not a business svr (no shm, no addr-based CLI).
    # We handle its runtime dir + conf + start/stop scripts explicitly here.
    base_dir = os.path.join(RUN_PATH, SD_AGENT_NAME)
    for sub in ('bin', 'conf', 'log'):
        subdir = os.path.join(base_dir, sub)
        if not os.path.exists(subdir):
            os.makedirs(subdir)

    # process yaml templates under cfg/sdagent/
    tmpl_path = os.path.join(CFG_PATH, SD_AGENT_NAME)
    yaml_path = os.path.join(base_dir, 'conf')
    if os.path.isdir(tmpl_path):
        for root, dirs, files in os.walk(tmpl_path):
            for filename in fnmatch.filter(files, '*.yaml_template'):
                yaml_filename = filename.replace('.yaml_template', '.yaml')
                yaml_filepath = os.path.join(yaml_path, yaml_filename)

                tmpl_data = yaml_load(os.path.join(tmpl_path, filename))
                if tmpl_data is None:
                    with open(yaml_filepath, "w") as file:
                        pass
                    continue

                data = yaml_replace_placeholders(tmpl_data, home_data)
                if data is None:
                    print(f">>> Yaml {yaml_filepath} generate failed")
                    return -1

                sd_override = (home_data.get('service_discovery') or {}).get('agent') or {}
                for k, v in sd_override.items():
                    if k in data:
                        data[k] = v

                yaml_save(yaml_filepath, data)

    # generate start/stop scripts
    sd_cfg = (home_data.get('service_discovery') or {}).get('agent') or {}
    key = sd_cfg.get('key') or ''
    create_sdagent_scripts(base_dir, key)

    formatted_key = "{:<20}".format(SD_AGENT_NAME)
    print(f">>> {formatted_key} files generate success")
    return 0

def create_sdagent_scripts(base_dir, key):
    bin_dir = os.path.join(base_dir, 'bin')
    suffix = f"_{key}" if key else ""
    key_flag = f"--key {key} " if key else ""

    def write_script(file_path, content):
        with open(file_path, 'w') as file:
            file.write(content)
        os.chmod(file_path, 0o755)

    start_args = (
        f"--conf-file ../conf/sdagent.yaml "
        f"--pid-file sdagent{suffix}.pid "
        f"--log-file ../log/sdagent{suffix}.log "
        f"{key_flag}"
    )

    start_script =  f"#!/bin/sh\n"
    start_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
    start_script += f"cd $DIR\n"
    start_script += f"./sdagent {start_args}--daemon start >>../log/sdagent.stdout.log 2>>../log/sdagent.stderr.log\n"
    write_script(os.path.join(bin_dir, 'start.sh'), start_script)

    stop_script =  f"#!/bin/sh\n"
    stop_script += f"DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
    stop_script += f"cd $DIR\n"
    stop_script += f"if [ ! -f sdagent{suffix}.pid ]; then\n"
    stop_script += f"\techo \"sdagent{suffix}.pid not found, skip stop.\"\n"
    stop_script += f"\texit 0\n"
    stop_script += f"fi\n"
    stop_script += f"./sdagent --pid-file sdagent{suffix}.pid stop\n"
    write_script(os.path.join(bin_dir, 'stop.sh'), stop_script)

def clean_generated_files():
    safe_remove_file(COMM_YAML_PATH)
    safe_remove_file(HOME_YAML_PATH)

    home_data = yaml_load(HOME_TMPL_PATH)

    for key in home_data['svr_list'].keys():
        # remove all server yaml files
        yaml_path = os.path.join(RUN_PATH, key, "conf")

        for root, dirs, files in os.walk(yaml_path):
            for filename in fnmatch.filter(files, '*.yaml'):
                safe_remove_file(os.path.join(yaml_path, filename))

        # remove all server scripts
        bin_dir = os.path.join(RUN_PATH, key, 'bin')

        safe_remove_file(os.path.join(bin_dir, 'start.sh'))
        safe_remove_file(os.path.join(bin_dir, 'stop.sh'))
        safe_remove_file(os.path.join(bin_dir, 'clear.sh'))
        safe_remove_file(os.path.join(bin_dir, 'gdb_attach.sh'))
        safe_remove_file(os.path.join(bin_dir, 'pydbg_attach.sh'))

        formatted_key = "{:<12}".format(key)
        print(f">>> {formatted_key} generated files removed")

    # sdagent runtime (not in svr_list)
    sd_conf_dir = os.path.join(RUN_PATH, SD_AGENT_NAME, 'conf')
    if os.path.isdir(sd_conf_dir):
        for root, dirs, files in os.walk(sd_conf_dir):
            for filename in fnmatch.filter(files, '*.yaml'):
                safe_remove_file(os.path.join(sd_conf_dir, filename))
    sd_bin_dir = os.path.join(RUN_PATH, SD_AGENT_NAME, 'bin')
    safe_remove_file(os.path.join(sd_bin_dir, 'start.sh'))
    safe_remove_file(os.path.join(sd_bin_dir, 'stop.sh'))
    formatted_key = "{:<12}".format(SD_AGENT_NAME)
    print(f">>> {formatted_key} generated files removed")

# ------------------------------------------------------------------------------
# yaml operation functions
def yaml_load(filename):
    with open(filename, 'r', encoding='utf-8') as f:
        return yaml.safe_load(f)
    
def yaml_save(filename, data):
    with open(filename, 'w', encoding='utf-8') as f:
        yaml.dump(data, f, Dumper=CustomDumper, default_flow_style=False, indent=2)

def yaml_get_value(data, key):
    # split key by '.' to keys
    keys = key.split('.')

    # get value by keys, will search layer by layer
    for k in keys:
        if isinstance(data, dict) and k in data:
            data = data[k]
        else:
            return None
    return data

def yaml_replace_placeholders(template, data):
    # replace function definition
    # match '${var}'
    def replace_single(match):
        # get the match word of re
        key = match.group(1)

        # get value by key
        value = yaml_get_value(data, key)
        #print(f"key: {key}, value: {value}, type: {type(value)}")

        # list/dict need convert to yaml stream
        if isinstance(value, list):
            return yaml.dump(value, default_flow_style=True).strip()
        elif isinstance(value, dict):
            return yaml.dump(value, default_flow_style=True).strip()
        elif isinstance(value, str):
            return '\'' + value + '\''
        elif isinstance(value, bool):
            return str(value).lower()
        elif isinstance(value, int):
            return str(value)
        elif isinstance(value, float):
            return str(value)
        return str(value or f'\'${{{key}}}\'')
    
    # match '${var1}${var2}'
    def replace_multi(match):
        # get the match word of re
        key = match.group(1)

        # get value by key
        value = yaml_get_value(data, key)

        # if match multi, cannot be complex type(list/dict)
        if isinstance(value, list) or isinstance(value, dict):
            print(f"Error: key \'{key}\' is complex type(list/dict), witch cannot be used in multi match(e.g {{var1}}{{var2}})")
            return KeyError
        elif isinstance(value, str):
            return '\'' + value + '\''
        elif isinstance(value, bool):
            return str(value).lower()
        elif isinstance(value, int):
            return str(value)
        elif isinstance(value, float):
            return str(value)
        return str(value or f'${{{key}}}')
        
    # replace all placeholders
    tmpl_str = yaml.dump(template, default_flow_style=True)

    try:
        # 1. replace single, need attention quotation marks
        yaml_str = re.sub(r'\'\$\{\s*([a-zA-Z_][\w\.]*)\s*\}\'', replace_single, tmpl_str)

        # 2. replace multi
        yaml_str = re.sub(r'\$\{\s*([a-zA-Z_][\w\.]*)\s*\}', replace_multi, yaml_str)

    except Exception as e:
        print(f"Error: yaml replace failed, e={e}")
        return None

    # return result: yaml stream
    return yaml.safe_load(yaml_str) if yaml_str else None

def init_mongo(home_data):
    # 内网：只建 GameData_<world>（GlobalData/ToolsData 由全量入口 ops_mongo/run.sh 单独建）
    mongo_path = os.path.join(os.getcwd(), "ops_mongo")
    os.system(f"cd {mongo_path} && bash apply_db.sh gamedata -u '{home_data['db']['uri']}' -w {home_data['world_id']}")
    return 0

# ------------------------------------------------------------------------------
# tools functions

def main():
    # define parameters
    parser = argparse.ArgumentParser()
    parser.add_argument('-c', action='store_true', help='Clean all generated yaml files.')
    parser.add_argument('world_id', type=int, nargs='?', help='World id.')
    parser.add_argument('-tcp', type=int, nargs='?', help='Gatesvr bind tcp port.')
    parser.add_argument('-udp', type=int, nargs='?', help='Gatesvr bind udp port.')
    parser.add_argument('-v', type=str, help='version', default='0')
    parser.add_argument('-external_ip', type=str, help='Gatesvr external IP address (string).')
    parser.add_argument('-external_tcp_port', type=int, help='Gatesvr external TCP port number (integer).')
    parser.add_argument('-external_udp_port', type=int, help='Gatesvr external UDP port number (integer).')
    parser.add_argument('-public_world', type=int, help='Public world id (string).')
    parser.add_argument('-loginsvr_count', type=int, help='Number of loginsvr instances for userId-based static routing.')
    parser.add_argument('-env', type=str, help='env(Debug, Dev, Beta, Release)', default='Debug',  choices=['Test','Debug','Dev', 'Beta', 'Release', 'BetaJP','ReleaseJP'])

    # parse parameters
    args, unknown = parser.parse_known_args()
    # 打印未知参数
    if unknown:
        print(f"Unknown arguments: {unknown}")

    #top
    if args.env is not None:
        global ENV
        ENV = args.env
        global HOME_TMPL_PATH
        HOME_TMPL_PATH = os.path.join(CFG_HOME_PATH, f"{ENV}.home.yaml_template")

    # clean all generated files
    if args.c:
        clean_generated_files()
        return 0
    

    # get world id
    world_id = 0
    
    if args.world_id is not None:
        world_id = args.world_id
    else:
        print(f">>> World id not set, config failed!!!")
        return -1
    
    public_world = 0
    
    if args.public_world is not None:
        public_world = args.public_world
    else:
        public_world = getPublicWorld(world_id)

    if world_id < 0:
        print(f">>> World id {world_id} is invalid, need >= 0")
        return -1
    else:
        print(f">>> World id: {world_id}")

    # 同步更新 docker/.env 中的 WORLD_ID
    docker_env_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "docker", ".env")
    if os.path.exists(docker_env_path):
        with open(docker_env_path, 'r') as f:
            env_content = f.read()
        env_content = re.sub(r'^WORLD_ID=.*$', f'WORLD_ID={world_id}', env_content, flags=re.MULTILINE)
        with open(docker_env_path, 'w') as f:
            f.write(env_content)
        print(f">>> Updated docker/.env WORLD_ID={world_id}")

    # get gatesvr port
    gatesvr_tcp_port = DEFAULT_GATESVR_TCP_PORT
    if args.tcp is not None:
        gatesvr_tcp_port = args.tcp

    gatesvr_udp_port = DEFAULT_GATESVR_UDP_PORT
    if args.udp is not None:
        gatesvr_udp_port = args.udp

    # loginsvr count: cli arg > default(1)
    global LOGINSVR_COUNT
    if args.loginsvr_count is not None:
        LOGINSVR_COUNT = args.loginsvr_count
    else:
        LOGINSVR_COUNT = DEFAULT_LOGINSVR_COUNT

    # gatesvr external info
    global GATESVR_EXTERNAL_IP, GATESVR_EXTERNAL_UDP_PORT, GATESVR_EXTERNAL_TCP_PORT
    GATESVR_EXTERNAL_IP = args.external_ip
    GATESVR_EXTERNAL_TCP_PORT = args.external_tcp_port
    GATESVR_EXTERNAL_UDP_PORT = args.external_udp_port
    print(f"Pass UDP Port: {GATESVR_EXTERNAL_UDP_PORT}")
    print(f"Pass UDP IP: {GATESVR_EXTERNAL_IP}")
    print(f"Pass UDP IP: {GATESVR_EXTERNAL_TCP_PORT}")
    # init environment
    ret = init_environment()
    if ret != 0:
        print(f">>> Init environment failed, ret={ret}")
        return ret
    
    # generate home config
    home_data = generate_home_yaml(world_id, gatesvr_tcp_port, gatesvr_udp_port, public_world)
    if home_data is None:
        print(f">>> Generate home yaml failed")
        return -1
    
    # generate server list
    ret = generate_directory_structure(home_data)
    if ret != 0:
        print(f">>> Generate server list failed")
        return ret
    
    # copy files
    ret = copy_files()
    if ret != 0:
        print(f">>> Copy files failed, ret={ret}")
        return ret

    # client version
    versiondata = init_clientversion(args.v)
    home_data.update(versiondata)

    # generate comm config
    ret = generate_commconf_files(home_data)
    if ret != 0:
        print(f">>> Generate comm files failed, ret={ret}")
        return ret

    # generate server config
    ret = generate_server_files(home_data)
    if ret != 0:
        print(f">>> Generate server files failed, ret={ret}")
        return ret

    # generate sdagent runtime dir (not in svr_list)
    ret = generate_sdagent_files(home_data)
    if ret != 0:
        print(f">>> Generate sdagent files failed, ret={ret}")
        return ret

    ret = init_mongo(home_data)
    if ret != 0:
        print(f">>> init_mongo failed, ret={ret}")
        return ret
    
def getPublicWorld(world_id):
    mongodata = yaml_load(HOME_TMPL_PATH)['db']
    formatted_uri = "{uri}".format(uri=mongodata['uri'])
    myclient = None
    try:
        import pymongo

        myclient = pymongo.MongoClient(formatted_uri)

        if "GlobalData" not in myclient.list_database_names():
            raise BaseException("GlobalData is not in mongo db list")
        
        mydb = myclient["GlobalData"]
        if "Servers" not in mydb.list_collection_names():
            raise BaseException("Servers is not in mongo collection list of GlobalData")
        
        mycol = mydb["Servers"]
        server = mycol.find_one({"world_id": world_id})
        if server is None:
            print("No world id {wid} found in db, no public world id found, use local value 0".format(wid=world_id))
            return 0
        if "public_world" not in server:
            print("Server {sid} public world id not set in mongo, use local value 0".format(sid=world_id))
            return 0
        print("Server {sid} public world id found in mongo, use it. public world id {public_world}".format(sid=world_id, public_world=server["public_world"]))
        return server["public_world"]
    except BaseException as e:
        print("get public world by mongo error, no cfg pointed ,plz add to mongo server GlobalData.Servers:" + str(e))
        return 0
    finally:
        if myclient is not None:
            myclient.close()

if __name__ == '__main__':
    ret = main()
    exit(ret)
