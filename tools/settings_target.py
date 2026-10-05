#!/usr/bin/env python3
"""Generate absolute linker/relative extra-file paths for local TinyGo."""
import argparse,json,os
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument("--root",required=True);p.add_argument("--output",required=True);a=p.parse_args()
r=Path(__file__).resolve().parents[1]
spec={"inherits":["esp32s3-supermini"],"linkerscript":str(r/"targets/esp32s3-settings.ld"),"extra-files":[os.path.relpath(r/"targets/settings-vectors.S",a.root)],"ldflags":["-Map="+str(r/"build/local-settings.map"),"--wrap=espradio_netif_set_connected"]}
Path(a.output).write_text(json.dumps(spec,indent=2)+"\n")
