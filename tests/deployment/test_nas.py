"""Deployment contracts; Docker Compose is required, a daemon is not."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
ENV = {k: v for k, v in os.environ.items() if not k.startswith("OPENSURGE_")}


class NASDeploymentTests(unittest.TestCase):
    def test_profile_network_and_privilege_boundaries(self):
        profiles = [("qnap", "qnap/.env.example", "qnet")]
        profiles += [(name, f"nas/.env{suffix}.example", "macvlan") for name, suffix in
                     [("generic", ""), ("synology", ".synology"), ("fnos", ".fnos")]]
        for platform, example, driver in profiles:
            with self.subTest(platform=platform):
                folder = "qnap" if platform == "qnap" else "nas"
                output = subprocess.check_output([
                    "docker", "compose", "--env-file", str(ROOT / "deploy" / example),
                    "-f", str(ROOT / "deploy" / folder / "docker-compose.yml"),
                    "config", "--format", "json"], env=ENV, text=True)
                config = json.loads(output)
                self.assertEqual(list(config["services"]), ["opensurge"])
                service = config["services"]["opensurge"]
                self.assertFalse(service.get("privileged", False))
                self.assertNotEqual(service.get("network_mode"), "host")
                self.assertNotIn("SYS_ADMIN", service["cap_add"])
                self.assertNotIn("ports", service)
                self.assertTrue(any(v["target"] == "/data" for v in service["volumes"]))
                self.assertFalse(any("docker.sock" in v.get("source", "") or
                                     "ns/net" in v.get("source", "") for v in service["volumes"]))
                network = config["networks"]["opensurge_lan"]
                self.assertEqual(network["driver"], driver)
                if driver == "macvlan":
                    self.assertEqual(service["environment"]["OPENSURGE_NAS_PLATFORM"], platform)
                    self.assertEqual(network["ipam"]["config"][0]["ip_range"], "192.168.2.241/32")
                preflight = subprocess.run([
                    "sh", str(ROOT / "deploy" / folder / "preflight.sh"),
                    "--env-file", str(ROOT / "deploy" / example), "--static"],
                    env=ENV, capture_output=True, text=True)
                self.assertEqual(preflight.returncode, 0, preflight.stdout + preflight.stderr)

    def test_reject_unsafe_or_inconsistent_configuration(self):
        cases = [
            ("OPENSURGE_IP", "192.168.3.241"),
            ("OPENSURGE_IP", "192.168.2.1"),
            ("OPENSURGE_IP", "192.168.2.0"),
            ("OPENSURGE_IP", "192.168.2.255"),
            ("OPENSURGE_IP", "192.168.02.241"),
            ("OPENSURGE_GATEWAY", "192.168.2.255"),
            ("OPENSURGE_SUBNET", "192.168.2.4/24"),
            ("OPENSURGE_SUBNET", "192.168.2.0/31"),
            ("OPENSURGE_DATA_PATH", "/volume1"),
            ("OPENSURGE_DATA_PATH", "/volume1/docker"),
            ("OPENSURGE_DATA_PATH", "/volume1/docker/../"),
            ("OPENSURGE_DATA_PATH", "/volume1/docker/data:/other"),
            ("OPENSURGE_WEB_UID", "0"),
            ("OPENSURGE_NAS_PLATFORM", "unknown"),
            ("OPENSURGE_CONTAINER_INTERFACE", "ovs_eth0"),
        ]
        sample = (ROOT / "deploy/nas/.env.synology.example").read_text()
        for key, value in cases:
            with self.subTest(key=key, value=value), tempfile.TemporaryDirectory() as temp:
                env_file = Path(temp) / ".env"
                lines = [f"{key}={value}" if line.startswith(key + "=") else line
                         for line in sample.splitlines()]
                env_file.write_text("\n".join(lines) + "\n")
                result = subprocess.run([
                    "sh", str(ROOT / "deploy/nas/preflight.sh"),
                    "--env-file", str(env_file), "--static"],
                    env=ENV, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("[FAIL]", result.stderr)


if __name__ == "__main__":
    unittest.main()
