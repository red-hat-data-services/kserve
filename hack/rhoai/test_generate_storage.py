# /// script
# requires-python = ">=3.11"
# ///

"""Exercise the storage artifact generator through its command-line interface."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
GENERATOR_PATH = Path(__file__).with_name("generate_storage.py")
OUTPUTS = {
    "requirements.txt": "runtime==1.0 --hash=sha256:abc\n",
    "requirements-build-deps.txt": "wheel==1.0 --hash=sha256:def\n",
}
CONFIDENTIAL_REQUIREMENT = "jwcrypto==1.5.0 --hash=sha256:123\n"
FAKE_UV = """\
import json
import os
from pathlib import Path
import sys

arguments = sys.argv[1:]
if arguments == ["--version"]:
    print("uv " + os.environ["FAKE_UV_VERSION"])
    sys.exit(0)
with open(os.environ["FAKE_UV_LOG"], "a") as log:
    overrides = Path(arguments[arguments.index("--override") + 1]).read_text()
    log.write(json.dumps({"arguments": arguments, "overrides": overrides}) + "\\n")
if os.environ.get("FAKE_UV_ERROR"):
    sys.exit(os.environ["FAKE_UV_ERROR"])
output = Path(arguments[arguments.index("-o") + 1])
body = json.loads(os.environ["FAKE_UV_OUTPUTS"])[output.name]
if "--extra" in arguments and arguments[arguments.index("--extra") + 1] == "confidential":
    body += os.environ["FAKE_UV_CONFIDENTIAL"]
output.write_text(body, encoding="utf-8")
"""


class GeneratorTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.storage = self.root / "python/storage"
        self.storage.mkdir(parents=True)
        self.script = self.root / "hack/rhoai/generate_storage.py"
        self.script.parent.mkdir(parents=True)
        shutil.copy2(GENERATOR_PATH, self.script)
        shutil.copy2(REPOSITORY_ROOT / "kserve-deps.env", self.root)
        for name in (
            "pyproject.toml",
            "konflux-hdfs.in",
            "konflux-build-deps.in",
            "konflux.rhoai.overrides",
        ):
            shutil.copy2(REPOSITORY_ROOT / "python/storage" / name, self.storage)

        tools = self.root / "bin"
        tools.mkdir()
        uv = tools / "uv"
        uv.write_text(f"#!{sys.executable}\n{FAKE_UV}", encoding="utf-8")
        uv.chmod(0o755)
        self.log = self.root / "uv-calls.jsonl"
        version = next(
            line.split("=", 1)[1]
            for line in (self.root / "kserve-deps.env").read_text().splitlines()
            if line.startswith("UV_VERSION=")
        )
        self.environment = {
            **os.environ,
            "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "FAKE_UV_VERSION": version,
            "FAKE_UV_LOG": str(self.log),
            "FAKE_UV_OUTPUTS": json.dumps(OUTPUTS),
            "FAKE_UV_CONFIDENTIAL": CONFIDENTIAL_REQUIREMENT,
        }

    def run_generator(self, *arguments):
        return subprocess.run(
            [sys.executable, str(self.script), *arguments],
            cwd=self.root,
            env=self.environment,
            capture_output=True,
            text=True,
        )

    def test_generates_both_artifacts_with_confidential_dependencies(self):
        result = self.run_generator()

        self.assertEqual(result.returncode, 0, result.stderr)
        for name, body in OUTPUTS.items():
            if name == "requirements.txt":
                body += CONFIDENTIAL_REQUIREMENT
            self.assertEqual((self.storage / name).read_text(), body)
        calls = map(json.loads, self.log.read_text().splitlines())
        runtime, build = (call["arguments"] for call in calls)
        self.assertIn("pyproject.toml", runtime)
        self.assertIn("konflux-hdfs.in", runtime)
        self.assertIn("konflux-build-deps.in", build)
        self.assertNotIn("--extra", build)
        for arguments in (runtime, build):
            self.assertEqual(arguments[:2], ["pip", "compile"])
            self.assertIn("--generate-hashes", arguments)
            for flag, expected in (
                ("--override", "konflux.rhoai.overrides"),
                ("--python-version", "3.12"),
                ("--python-platform", "x86_64-unknown-linux-gnu"),
            ):
                self.assertEqual(arguments[arguments.index(flag) + 1], expected)

    def test_passes_overrides_to_uv_without_a_duplicate_policy(self):
        overrides = "huggingface-hub>=0.32.0\npyjwt==2.13.0\nother-package==1.0\n"
        (self.storage / "konflux.rhoai.overrides").write_text(overrides)

        result = self.run_generator()

        self.assertEqual(result.returncode, 0, result.stderr)
        for call in map(json.loads, self.log.read_text().splitlines()):
            self.assertEqual(call["overrides"], overrides)

    def test_check_reports_missing_artifacts_without_writes(self):
        result = self.run_generator("--check")

        self.assertEqual(result.returncode, 1, result.stderr)
        for name in OUTPUTS:
            self.assertIn(name, result.stderr)
            self.assertFalse((self.storage / name).exists())

    def test_check_accepts_current_artifacts_and_preserves_stale_artifacts(self):
        result = self.run_generator()
        self.assertEqual(result.returncode, 0, result.stderr)
        current = {name: (self.storage / name).read_bytes() for name in OUTPUTS}

        result = self.run_generator("--check")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            {name: (self.storage / name).read_bytes() for name in OUTPUTS}, current
        )

        (self.storage / "requirements.txt").write_text("stale\n")
        result = self.run_generator("--check")

        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("requirements.txt", result.stderr)
        self.assertEqual((self.storage / "requirements.txt").read_text(), "stale\n")
        self.assertEqual(
            (self.storage / "requirements-build-deps.txt").read_bytes(),
            current["requirements-build-deps.txt"],
        )

    def test_uv_failure_is_reported_without_artifact_writes(self):
        for name in OUTPUTS:
            (self.storage / name).write_text("previous artifact\n")
        self.environment["FAKE_UV_ERROR"] = "dependency resolution failed"

        result = self.run_generator()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("dependency resolution failed", result.stderr)
        for name in OUTPUTS:
            self.assertEqual((self.storage / name).read_text(), "previous artifact\n")

    def test_rejects_unsafe_artifacts_without_writes(self):
        for name in OUTPUTS:
            (self.storage / name).write_text("previous artifact\n")
        for name in OUTPUTS:
            for unsafe in (
                "hf-transfer==1.0\n",
                "--index-url https://pypi.org/simple\n",
                "https://files.pythonhosted.org/package.whl\n",
            ):
                with self.subTest(artifact=name, unsafe=unsafe):
                    self.environment["FAKE_UV_OUTPUTS"] = json.dumps(
                        {**OUTPUTS, name: unsafe}
                    )

                    result = self.run_generator()

                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(name, result.stderr)
                    for artifact in OUTPUTS:
                        self.assertEqual(
                            (self.storage / artifact).read_text(), "previous artifact\n"
                        )

    def test_rejects_wrong_uv_version_before_compilation(self):
        self.environment["FAKE_UV_VERSION"] = "0.0.0"

        result = self.run_generator()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("expected uv", result.stderr)
        self.assertFalse(self.log.exists())

    def test_rejects_changed_upstream_requirements_before_compilation(self):
        pyproject = self.storage / "pyproject.toml"
        original = pyproject.read_text()
        for requirement in (
            "huggingface-hub>=0.32.0",
            "huggingface-hub[hf-transfer]>=0.33.0",
        ):
            with self.subTest(requirement=requirement):
                pyproject.write_text(
                    original.replace(
                        "huggingface-hub[hf-transfer]>=0.32.0", requirement
                    )
                )

                result = self.run_generator()

                self.assertNotEqual(result.returncode, 0)
                self.assertIn("ODH-verbatim", result.stderr)
                self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main()
