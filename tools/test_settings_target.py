#!/usr/bin/env python3
"""Regression checks for default layout and explicit Make target overrides."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

class TargetTests(unittest.TestCase):
    def recipe(self, rule, target=None):
        args = ["make", "-n", rule]
        if target is not None:
            args.append("TINYGO_TARGET=" + target)
        return subprocess.check_output(args, cwd=ROOT, text=True)

    def test_local_default_preserves_settings_layout(self):
        for target in [None, "targets/esp32s3-settings.json", "/src/targets/esp32s3-settings.json"]:
            recipe = self.recipe("firmware-local", target)
            self.assertIn("tools/settings_target.py", recipe)
            self.assertIn('-target="build/local-target.json"', recipe)
            self.assertIn("tools/settings_image.py", recipe)
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "target.json"
            subprocess.check_call(["python3", "tools/settings_target.py", "--root", folder, "--output", str(output)], cwd=ROOT)
            spec = json.loads(output.read_text())
            self.assertEqual(spec["inherits"], ["esp32s3-supermini"])
            self.assertEqual(spec["linkerscript"], str(ROOT / "targets/esp32s3-settings.ld"))
            self.assertEqual((Path(folder) / spec["extra-files"][0]).resolve(), ROOT / "targets/settings-vectors.S")
            self.assertIn("--wrap=espradio_netif_set_connected", spec["ldflags"])
        docker_spec = json.loads((ROOT / "targets/esp32s3-settings.json").read_text())
        self.assertIn("--wrap=espradio_netif_set_connected", docker_spec["ldflags"])

    def test_local_override_is_not_replaced(self):
        for target in ["custom-settings.json", "/opt/board/target.json", "esp32s3-supermini"]:
            recipe = self.recipe("firmware-local", target)
            self.assertNotIn("tools/settings_target.py", recipe)
            self.assertNotIn("build/local-target.json", recipe)
            self.assertIn('-target="' + target + '"', recipe)
            self.assertIn("tools/settings_image.py", recipe)

    def test_docker_override_remains_honored(self):
        self.assertIn("-target=/src/targets/esp32s3-settings.json", self.recipe("firmware"))
        self.assertIn("-target=/src/custom-settings.json", self.recipe("firmware", "/src/custom-settings.json"))

if __name__ == "__main__":
    unittest.main()
