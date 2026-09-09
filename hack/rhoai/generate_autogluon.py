# /// script
# requires-python = ">=3.11"
# dependencies = ["tomlkit==0.13.2"]
# ///

"""Generate the downstream RHOAI AutoGluon dependency artifacts."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from typing import Any
from urllib.parse import urlsplit

import tomlkit


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
DEPENDENCY_ENV = REPOSITORY_ROOT / "kserve-deps.env"
DEFAULT_PROJECT = REPOSITORY_ROOT / "python/autogluonserver/pyproject.rhoai.toml"
DEFAULT_OUTPUT_DIR = REPOSITORY_ROOT / "python/autogluonserver"
CONTENT_OUTPUTS = (
    "uv.rhoai.lock",
    "autogluon-all-requirements.txt",
)
RHOAI_PROJECT = "pyproject.rhoai.toml"
AIPCC_ARTIFACT_HOST = "packages.redhat.com"
LOCAL_PACKAGE_SOURCES = {
    ("autogluonserver", "editable", "."),
    ("kserve", "directory", "../kserve"),
    ("kserve-storage", "virtual", "../storage"),
}
KONFLUX_PLATFORM_MARKERS = {
    "linux_aarch64",
    "linux_ppc64le",
    "linux_s390x",
    "linux_x86_64",
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


def _table(value: Any, name: str) -> Any:
    if not hasattr(value, "keys"):
        raise GenerationError(f"{name} must be a TOML table")
    return value


def _validate_project(project_path: Path) -> str:
    project = tomlkit.parse(project_path.read_text(encoding="utf-8"))
    project_table = _table(project.get("project"), "project")
    if project_table.get("name") != "autogluonserver":
        raise GenerationError("project.name must be autogluonserver")
    if project_table.get("requires-python") != ">=3.11,<3.13":
        raise GenerationError("RHOAI project must target Python >=3.11,<3.13")

    dependencies = project_table.get("dependencies")
    if not dependencies or not all(isinstance(item, str) for item in dependencies):
        raise GenerationError("project.dependencies must be a non-empty string array")
    required_packages = ("autogluon.tabular", "autogluon.timeseries")
    if not all(
        any(dep.startswith(package) for dep in dependencies)
        for package in required_packages
    ):
        raise GenerationError("both patched AutoGluon packages are required")

    build_system = _table(project.get("build-system"), "build-system")
    if build_system.get("requires") != ["setuptools>=61.0"]:
        raise GenerationError("RHOAI build-system must require setuptools>=61.0")
    if build_system.get("build-backend") != "setuptools.build_meta":
        raise GenerationError("RHOAI build-system must use setuptools.build_meta")

    dependency_groups = _table(project.get("dependency-groups"), "dependency-groups")
    if set(dependency_groups) != {"rhoai-build"}:
        raise GenerationError("RHOAI project must contain only the rhoai-build group")
    if dependency_groups["rhoai-build"] != ["setuptools>=61.0", "wheel"]:
        raise GenerationError("rhoai-build must contain setuptools and wheel")

    tool = _table(project.get("tool"), "tool")
    uv = _table(tool.get("uv"), "tool.uv")
    if set(uv) != {"index", "sources"}:
        raise GenerationError("tool.uv must contain only index and sources")

    indexes = uv["index"]
    if (
        len(indexes) != 1
        or set(indexes[0]) != {"name", "url", "default"}
        or indexes[0].get("default") is not True
    ):
        raise GenerationError("RHOAI project must define one default index")
    index_name = str(indexes[0].get("name", ""))
    index_url = str(indexes[0].get("url", ""))
    parsed_url = urlsplit(index_url)
    if parsed_url.scheme != "https" or not parsed_url.netloc:
        raise GenerationError("the RHOAI index must use an absolute HTTPS URL")
    if parsed_url.username or parsed_url.password:
        raise GenerationError("the RHOAI index URL must not contain credentials")

    sources = _table(uv["sources"], "tool.uv.sources")
    expected_sources = {
        "autogluon.common",
        "autogluon.core",
        "autogluon.features",
        "autogluon.tabular",
        "autogluon.timeseries",
        "kserve",
        "kserve-storage",
    }
    if set(sources) != expected_sources:
        raise GenerationError(
            "tool.uv.sources keys must be "
            f"{sorted(expected_sources)}; got {sorted(sources)}"
        )

    expected_local_sources = {
        "kserve": {"path": "../kserve", "editable": False},
        "kserve-storage": {"path": "../storage", "editable": False},
    }
    for package, expected in expected_local_sources.items():
        source = _table(sources[package], f"tool.uv.sources.{package}")
        if dict(source) != expected:
            raise GenerationError(f"{package} must use the local source {expected}")

    for package, source in sources.items():
        if package in expected_local_sources:
            continue
        if set(_table(source, f"tool.uv.sources.{package}").keys()) != {"index"}:
            raise GenerationError(f"{package} must declare only an index source")
        if source["index"] != index_name:
            raise GenerationError(f"{package} must use the RHOAI index")
    return index_url


def _validate_lock(lock: bytes, index_url: str) -> None:
    document = tomlkit.parse(lock.decode("utf-8"))
    registry_urls = set[str]()
    platform_markers = set[str]()
    for package in document.get("package", []):
        name = str(package["name"])
        source = package.get("source", {})
        if "registry" in source:
            if set(source.keys()) != {"registry"}:
                raise GenerationError(f"{name} has an invalid registry source")
            registry_urls.add(str(source["registry"]))
        elif len(source) == 1:
            source_kind, source_value = next(iter(source.items()))
            if (name, source_kind, str(source_value)) not in LOCAL_PACKAGE_SOURCES:
                raise GenerationError(f"{name} has a non-local package source")
        else:
            raise GenerationError(f"{name} has an invalid package source")

        artifacts = list(package.get("wheels", []))
        if "sdist" in package:
            artifacts.append(package["sdist"])
        for artifact in artifacts:
            url = str(artifact["url"])
            parsed_url = urlsplit(url)
            if parsed_url.scheme != "https" or parsed_url.netloc != AIPCC_ARTIFACT_HOST:
                raise GenerationError(
                    f"{name} has an artifact outside {AIPCC_ARTIFACT_HOST}"
                )
            platform_markers.update(
                marker for marker in KONFLUX_PLATFORM_MARKERS if marker in url
            )

    if registry_urls != {index_url}:
        raise GenerationError(
            "RHOAI lock contains a non-RHOAI package source: "
            + ", ".join(sorted(registry_urls))
        )
    if platform_markers != KONFLUX_PLATFORM_MARKERS:
        raise GenerationError(
            "RHOAI lock is missing Konflux platform artifacts: "
            + ", ".join(sorted(KONFLUX_PLATFORM_MARKERS - platform_markers))
        )
    if b"pypi.org" in lock or b"pythonhosted.org" in lock:
        raise GenerationError("RHOAI lock contains public PyPI artifact URLs")


def _validate_requirements(requirements: bytes) -> None:
    current = ""
    for line in requirements.decode("utf-8").splitlines():
        if re.match(r"^[A-Za-z0-9_.-]+==", line):
            if current and "--hash=sha256:" not in current:
                raise GenerationError("exported requirement is missing a hash")
            current = line
        elif current:
            current += "\n" + line
    if current and "--hash=sha256:" not in current:
        raise GenerationError("exported requirement is missing a hash")


def _run_uv(project_dir: Path) -> tuple[bytes, bytes]:
    version = subprocess.run(
        ["uv", "--version"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    if version != f"uv {UV_VERSION}":
        raise GenerationError(f"expected uv {UV_VERSION}, got {version}")

    environment = os.environ.copy()
    environment["UV_NO_PROGRESS"] = "1"
    subprocess.run(["uv", "lock"], cwd=project_dir, check=True, env=environment)
    subprocess.run(
        ["uv", "lock", "--check"], cwd=project_dir, check=True, env=environment
    )
    requirements_body = project_dir / "requirements.body.txt"
    subprocess.run(
        [
            "uv",
            "export",
            "--locked",
            "--format",
            "requirements.txt",
            "--no-dev",
            "--group",
            "rhoai-build",
            "--no-header",
            "--no-emit-project",
            "--no-emit-package",
            "kserve",
            "--no-emit-package",
            "kserve-storage",
            "--output-file",
            str(requirements_body),
        ],
        cwd=project_dir,
        check=True,
        env=environment,
        stdout=subprocess.DEVNULL,
    )
    return (project_dir / "uv.lock").read_bytes(), requirements_body.read_bytes()


def _generate(
    project_path: Path,
    output_dir: Path,
) -> dict[str, bytes]:
    index_url = _validate_project(project_path)
    project = project_path.read_bytes()
    python_dir = project_path.parent.parent
    for local_project in ("kserve", "storage"):
        if not (python_dir / local_project / "pyproject.toml").is_file():
            raise GenerationError(f"missing local project: {local_project}")

    with tempfile.TemporaryDirectory(prefix="autogluon-rhoai-") as temp_dir:
        temp_python = Path(temp_dir) / "python"
        temp_python.mkdir()
        ignored = shutil.ignore_patterns(
            ".venv", "__pycache__", RHOAI_PROJECT, *CONTENT_OUTPUTS
        )
        for local_project in ("kserve", "storage"):
            shutil.copytree(
                python_dir / local_project,
                temp_python / local_project,
                ignore=ignored,
            )
        temp_project = temp_python / project_path.parent.name
        shutil.copytree(project_path.parent, temp_project, ignore=ignored)
        (temp_project / "pyproject.toml").write_bytes(project)
        existing_lock = output_dir / "uv.rhoai.lock"
        if existing_lock.is_file():
            shutil.copy2(existing_lock, temp_project / "uv.lock")
        lock, requirements_body = _run_uv(temp_project)

    _validate_lock(lock, index_url)

    if b"--index-url" in requirements_body or b"--extra-index-url" in requirements_body:
        raise GenerationError("uv export unexpectedly emitted an index directive")
    _validate_requirements(requirements_body)
    if not requirements_body.endswith(b"\n"):
        requirements_body += b"\n"
    requirements = f"--index-url {index_url}\n\n".encode() + requirements_body

    outputs = {
        "uv.rhoai.lock": lock,
        "autogluon-all-requirements.txt": requirements,
    }
    return outputs


def _apply_outputs(outputs: dict[str, bytes], output_dir: Path, check: bool) -> int:
    changed = [
        name
        for name, content in outputs.items()
        if not (output_dir / name).is_file()
        or (output_dir / name).read_bytes() != content
    ]
    if check:
        if changed:
            print(
                "generated AutoGluon artifacts differ: " + ", ".join(changed),
                file=sys.stderr,
            )
            return 1
        print("generated AutoGluon artifacts are current")
        return 0

    output_dir.mkdir(parents=True, exist_ok=True)
    for name, content in outputs.items():
        destination = output_dir / name
        with tempfile.NamedTemporaryFile(dir=output_dir, delete=False) as temporary:
            temporary.write(content)
            temporary_path = Path(temporary.name)
        temporary_path.replace(destination)
    print("updated AutoGluon artifacts: " + ", ".join(outputs))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", type=Path, default=DEFAULT_PROJECT)
    parser.add_argument("--output-dir", type=Path, default=DEFAULT_OUTPUT_DIR)
    parser.add_argument("--check", action="store_true")
    arguments = parser.parse_args()

    try:
        outputs = _generate(
            arguments.project.resolve(),
            arguments.output_dir.resolve(),
        )
        return _apply_outputs(outputs, arguments.output_dir.resolve(), arguments.check)
    except (GenerationError, tomlkit.exceptions.ParseError) as error:
        parser.error(str(error))
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
