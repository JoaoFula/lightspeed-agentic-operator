"""Cluster-free tests for the disconnected provisioning entrypoint."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("e2e-disconnected.sh")


class EntrypointTests(unittest.TestCase):
    def test_output_redacts_credentials(self):
        result = subprocess.run(["python3", str(SCRIPT.with_name("e2e-redact.py"))], input="model output includes vllm-sensitive and hf-sensitive\n", env={**os.environ, "VLLM_API_KEY": "vllm-sensitive", "HUGGING_FACE_HUB_TOKEN": "hf-sensitive"}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "model output includes [REDACTED] and [REDACTED]\n")

    def test_independent_cleanup_uses_invocation_label(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            oc = root / "oc"
            oc.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$COMMAND_LOG"\ncase "$*" in *"-o yaml"*) echo cleanup-sensitive ;; esac\n')
            oc.chmod(0o755)
            env = {**os.environ, "PATH": f"{root}:{os.environ['PATH']}", "E2E_DISCONNECTED_ID": "test-invocation", "VLLM_API_KEY": "cleanup-sensitive", "ARTIFACT_DIR": str(root / "artifacts"), "COMMAND_LOG": str(root / "commands")}
            result = subprocess.run(["bash", str(SCRIPT.with_name("e2e-disconnected-cleanup.sh"))], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            commands = (root / "commands").read_text()
            self.assertIn("delete pods,secrets,networkpolicies,serviceaccounts,rolebindings", commands)
            self.assertIn("agentic.openshift.io/disconnected-e2e=test-invocation", commands)
            artifacts = (root / "artifacts/disconnected/fallback/owned-resources.yaml").read_text()
            self.assertIn("[REDACTED]", artifacts)
            self.assertNotIn("cleanup-sensitive", artifacts)

    def test_requires_full_service_commit(self):
        for ref in ("", "main", "abc123", "a" * 39, "a" * 41, "g" * 40, "a" * 40 + "^{commit}"):
            with self.subTest(ref=ref):
                result = subprocess.run(["bash", str(SCRIPT)], env={**os.environ, "LIGHTSPEED_SERVICE_REF": ref}, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("40 hexadecimal", result.stderr)

    def test_provisioning_handoff_and_failure_preservation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            oc = bin_dir / "oc"
            oc.write_text("#!/bin/sh\nexit 0\n")
            oc.chmod(0o755)
            git = bin_dir / "git"
            git.write_text('''#!/bin/bash
set -eu
if [[ "$1" == clone ]]; then
 dest="${@: -1}"
 mkdir -p "$dest/tests/rhoai/scripts"
 cp "$PROVISION_STUB" "$dest/tests/rhoai/scripts/provision-vllm.sh"
elif [[ "$3" == cat-file ]]; then
 exit "${PIN_CHECK_RC:-0}"
elif [[ "$3" == rev-parse ]]; then
 echo "${GIT_HEAD:-$LIGHTSPEED_SERVICE_REF}"
fi
''')
            git.chmod(0o755)
            provision = root / "provision.sh"
            provision.write_text('''#!/bin/bash
set -eu
[[ "$1 $2 $3" == "--profile gemma4 --output-env" ]]
printf '%s\\n' \\
 'RHOAI_VLLM_BASE_URL=http://vllm.models.svc:8000/v1' \\
 'RHOAI_VLLM_MODEL=google/gemma-4-31B-it' > "$4"
''')
            provision.chmod(0o755)
            wrapper = bin_dir / "bash"
            wrapper.write_text('''#!/bin/bash
if [[ "$1" == */e2e-cluster.sh ]]; then
 [[ "$2" == openai && "$E2E_SCENARIO_TAGS" == core && "$E2E_DISCONNECTED" == true ]]
 [[ "$E2E_OPENAI_URL" == http://vllm.models.svc:8000/v1 ]]
 [[ "$(< "$OPENAI_PROVIDER_KEY_PATH")" == test-secret ]]
 touch "$ENTRYPOINT_REACHED"
 exit 7
fi
exec /bin/bash "$@"
''')
            wrapper.chmod(0o755)
            env = {**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}", "LIGHTSPEED_SERVICE_REF": "a" * 40, "PROVISION_STUB": str(provision), "HUGGING_FACE_HUB_TOKEN": "hf-secret", "VLLM_API_KEY": "test-secret", "SANDBOX_IMAGE": "image-registry.openshift-image-registry.svc:5000/test/sandbox:v1", "ENTRYPOINT_REACHED": str(root / "reached")}
            result = subprocess.run(["/bin/bash", str(SCRIPT)], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 7, result.stderr)
            self.assertTrue((root / "reached").exists())
            self.assertNotIn("test-secret", result.stdout + result.stderr)
            self.assertNotIn("hf-secret", result.stdout + result.stderr)
            for overrides, expected in (({"GIT_HEAD": "b" * 40}, 1), ({"PIN_CHECK_RC": "9"}, 9)):
                (root / "reached").unlink(missing_ok=True)
                failure = subprocess.run(["/bin/bash", str(SCRIPT)], env={**env, **overrides}, capture_output=True, text=True)
                self.assertEqual(failure.returncode, expected, failure.stderr)
                self.assertFalse((root / "reached").exists())


if __name__ == "__main__":
    unittest.main()
