# /// script
# requires-python = ">=3.11"
# dependencies = ["tomlkit==0.13.2"]
# ///

"""Tests for the RHOAI AutoGluon artifact generator contract."""

from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
PROJECT_PATH = REPOSITORY_ROOT / "python/autogluonserver/pyproject.rhoai.toml"
LOCK_PATH = REPOSITORY_ROOT / "python/autogluonserver/uv.rhoai.lock"
GENERATOR_PATH = Path(__file__).with_name("generate_autogluon.py")

spec = importlib.util.spec_from_file_location("generate_autogluon", GENERATOR_PATH)
if spec is None or spec.loader is None:
    raise RuntimeError(f"cannot load {GENERATOR_PATH}")
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)


class GeneratorContractTests(unittest.TestCase):
    def test_checked_in_project_and_lock_pass_validation(self):
        index_url = generator._validate_project(PROJECT_PATH)

        generator._validate_lock(LOCK_PATH.read_bytes(), index_url)

    def test_project_rejects_credentials_in_index_url(self):
        with tempfile.TemporaryDirectory() as directory:
            project_path = Path(directory) / "pyproject.rhoai.toml"
            project = PROJECT_PATH.read_text(encoding="utf-8")
            project_path.write_text(
                project.replace(
                    "https://console.redhat.com/",
                    "https://user:password@console.redhat.com/",
                ),
                encoding="utf-8",
            )

            with self.assertRaisesRegex(
                generator.GenerationError, "must not contain credentials"
            ):
                generator._validate_project(project_path)

    def test_lock_rejects_artifacts_outside_allowed_host(self):
        index_url = generator._validate_project(PROJECT_PATH)
        lock = LOCK_PATH.read_bytes().replace(
            b"https://packages.redhat.com/",
            b"https://files.pythonhosted.org/",
            1,
        )

        with self.assertRaisesRegex(
            generator.GenerationError, "artifact outside packages.redhat.com"
        ):
            generator._validate_lock(lock, index_url)

    def test_requirements_require_hashes(self):
        with self.assertRaisesRegex(generator.GenerationError, "missing a hash"):
            generator._validate_requirements(b"package==1.0\n")

    def test_check_mode_detects_and_preserves_stale_outputs(self):
        outputs = {"one.txt": b"one\n", "two.txt": b"two\n"}
        with tempfile.TemporaryDirectory() as directory:
            output_dir = Path(directory)

            self.assertEqual(generator._apply_outputs(outputs, output_dir, True), 1)
            self.assertFalse((output_dir / "one.txt").exists())

            self.assertEqual(generator._apply_outputs(outputs, output_dir, False), 0)
            self.assertEqual(generator._apply_outputs(outputs, output_dir, True), 0)

            (output_dir / "one.txt").write_text("stale\n", encoding="utf-8")
            self.assertEqual(generator._apply_outputs(outputs, output_dir, True), 1)
            self.assertEqual(
                (output_dir / "one.txt").read_text(encoding="utf-8"), "stale\n"
            )


if __name__ == "__main__":
    unittest.main()
