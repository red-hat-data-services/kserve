# /// script
# requires-python = ">=3.11"
# dependencies = ["tomlkit==0.13.2"]
# ///

"""Generate the downstream RHOAI storage-initializer dependency artifacts.

The shared python/storage/pyproject.toml is synchronized from ODH and must
stay ODH-verbatim; the downstream hermetic delta lives in
python/storage/konflux.rhoai.overrides. This generator compiles:

- requirements.txt (runtime deps, incl. the konflux-hdfs.in backend deps);
- requirements-build-deps.txt (wheel build deps from konflux-build-deps.in);

against the AIPCC index with the repo-pinned uv version. Use --check to
validate that the committed artifacts are current.
"""

from __future__ import annotations

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import tomlkit

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
DEPENDENCY_ENV = REPOSITORY_ROOT / "kserve-deps.env"
STORAGE_DIR = REPOSITORY_ROOT / "python/storage"
OVERRIDES_FILE = STORAGE_DIR / "konflux.rhoai.overrides"
BUILD_DEPS_IN = STORAGE_DIR / "konflux-build-deps.in"
HDFS_IN = STORAGE_DIR / "konflux-hdfs.in"
SHARED_PYPROJECT = STORAGE_DIR / "pyproject.toml"
CONTENT_OUTPUTS = (
    "requirements.txt",
    "requirements-build-deps.txt",
)

# The AIPCC index the hermetic artifacts compile against. Keep in sync with
# the Konflux PipelineRun image stream.
AIPCC_INDEX = (
    "https://packages.redhat.com/api/pypi/public-rhai/rhoai/3.6-EA1/cpu-ubi9/simple/"
)

# Every override must be declared here with the substring its rationale
# comment has to contain, so silent unreviewed deltas cannot accumulate.
EXPECTED_OVERRIDES = {
    "huggingface-hub": "AIPCC-18230",
    "pyjwt": "2.14.x",
}


class GenerationError(RuntimeError):
    """An invalid input or generated artifact."""


def _read_dependency_version(name: str) -> str:
    """Read one simple NAME=value assignment without executing the env file."""
    for raw_line in DEPENDENCY_ENV.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        match = re.fullmatch(rf"{re.escape(name)}=([^\s#]+)", line)
        if match:
            return match.group(1)
    raise GenerationError(f"{name} is not defined in {DEPENDENCY_ENV.name}")


UV_VERSION = _read_dependency_version("UV_VERSION")


def _validate_shared_pyproject() -> None:
    """Guard the ODH-verbatim contract of the shared pyproject."""
    project = tomlkit.parse(SHARED_PYPROJECT.read_text(encoding="utf-8"))
    dependencies = project.get("project", {}).get("dependencies", [])
    hub = [d for d in dependencies if str(d).startswith("huggingface-hub")]
    if hub != ["huggingface-hub[hf-transfer]>=0.32.0"]:
        raise GenerationError(
            "python/storage/pyproject.toml must declare the ODH-verbatim "
            "huggingface-hub[hf-transfer] requirement; downstream deltas "
            "belong in konflux.rhoai.overrides"
        )


def _validate_overrides() -> None:
    lines = OVERRIDES_FILE.read_text(encoding="utf-8").splitlines()
    requirements: list[str] = []
    preceding_comments: list[str] = []
    comment_run: list[str] = []
    for line in lines:
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith("#"):
            comment_run.append(stripped.lstrip("# ").strip())
        else:
            requirements.append(stripped)
            preceding_comments.append("\n".join(comment_run))
            comment_run = []

    names = []
    for requirement in requirements:
        match = re.match(r"^([A-Za-z0-9_.-]+)", requirement)
        if not match:
            raise GenerationError(f"unparseable override: {requirement}")
        names.append(match.group(1).lower())

    if sorted(names) != sorted(EXPECTED_OVERRIDES):
        raise GenerationError(
            "konflux.rhoai.overrides must declare exactly "
            f"{sorted(EXPECTED_OVERRIDES)}; got {sorted(names)}. Update "
            "EXPECTED_OVERRIDES in generate_storage.py together with the "
            "override file so every delta is reviewed."
        )
    for name, comments in zip(names, preceding_comments):
        marker = EXPECTED_OVERRIDES[name]
        if marker not in comments:
            raise GenerationError(
                f"override {name} is missing a rationale comment containing "
                f"'{marker}' (when it can be dropped and why it exists)"
            )


def _run_uv(arguments: list[str], workdir: Path) -> None:
    version = subprocess.run(
        ["uv", "--version"], check=True, capture_output=True, text=True
    ).stdout.strip()
    if version.split(maxsplit=2)[:2] != ["uv", UV_VERSION]:
        raise GenerationError(f"expected uv {UV_VERSION}, got {version}")

    environment = os.environ.copy()
    environment["UV_NO_PROGRESS"] = "1"
    result = subprocess.run(
        ["uv", *arguments],
        cwd=workdir,
        env=environment,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise GenerationError(
            "uv failed to compile the RHOAI artifacts:\n"
            f"{result.stdout}\n{result.stderr}"
        )


def _compile_arguments(output: str, sources: list[str]) -> list[str]:
    return [
        "pip",
        "compile",
        *sources,
        "--override",
        OVERRIDES_FILE.name,
        "--index-url",
        AIPCC_INDEX,
        "--emit-index-url",
        "--generate-hashes",
        "--python-version",
        "3.12",
        "--python-platform",
        "x86_64-unknown-linux-gnu",
        "-o",
        output,
    ]


COMPILE_INPUTS = (
    SHARED_PYPROJECT,
    HDFS_IN,
    BUILD_DEPS_IN,
    OVERRIDES_FILE,
)


def _generate(output_dir: Path) -> dict[str, bytes]:
    _validate_shared_pyproject()
    _validate_overrides()

    # Compile inside a temp copy of the inputs so the autogenerated command
    # header records stable relative paths and the output is deterministic.
    with tempfile.TemporaryDirectory(prefix="storage-rhoai-") as temp_dir:
        temp_dir = Path(temp_dir)
        for input_file in COMPILE_INPUTS:
            shutil.copy2(input_file, temp_dir / input_file.name)
        runtime_output = temp_dir / "requirements.txt"
        build_output = temp_dir / "requirements-build-deps.txt"
        _run_uv(
            _compile_arguments(
                runtime_output.name, [SHARED_PYPROJECT.name, HDFS_IN.name]
            ),
            temp_dir,
        )
        _run_uv(
            _compile_arguments(build_output.name, [BUILD_DEPS_IN.name]),
            temp_dir,
        )
        generated = {
            output.name: output.read_bytes()
            for output in (runtime_output, build_output)
        }

    for name, body in generated.items():
        if b"hf-transfer" in body:
            raise GenerationError(f"{name} still contains hf-transfer")
        if b"pypi.org" in body or b"pythonhosted.org" in body:
            raise GenerationError(f"{name} contains public PyPI artifact URLs")
    return generated


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="verify the committed artifacts are current instead of writing",
    )
    arguments = parser.parse_args()
    output_dir = STORAGE_DIR
    generated = _generate(output_dir)
    if not arguments.check:
        for name in CONTENT_OUTPUTS:
            (output_dir / name).write_bytes(generated[name])
        print(f"regenerated {', '.join(CONTENT_OUTPUTS)}")
        return 0

    stale = []
    for name in CONTENT_OUTPUTS:
        committed = (output_dir / name).read_bytes() if (output_dir / name).is_file() else b""
        if committed != generated[name]:
            stale.append(name)
    if stale:
        print(
            "committed artifacts are stale: "
            + ", ".join(stale)
            + f"\nrun: uv run {Path(__file__).name}",
            file=sys.stderr,
        )
        return 1
    print("committed artifacts are current")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
