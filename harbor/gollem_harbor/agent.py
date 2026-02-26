"""Harbor adapter for the gollem coding agent.

This module implements the BaseInstalledAgent interface so gollem can be
evaluated on Terminal-Bench 2.0 via Harbor.

Usage:
    # First, build the Linux binary:
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o harbor/gollem-linux-amd64 ./cmd/gollem/

    # Then run:
    harbor run -d terminal-bench@2.0 \\
        --agent-import-path gollem_harbor:GollemAgent \\
        -m anthropic/claude-sonnet-4-6 \\
        --env docker
"""

from __future__ import annotations

import json
import logging
import os
import shlex
import shutil
import subprocess
from pathlib import Path

from harbor.agents.installed.base import BaseInstalledAgent, ExecInput
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

_PKG_DIR = Path(__file__).parent
_TEMPLATES_DIR = _PKG_DIR / "templates"
_REPO_ROOT = _PKG_DIR.parent.parent  # gollem repo root
_TASK_CACHE_DIR = Path.home() / ".cache" / "harbor" / "tasks"
_AGENT_VENV_PATH = "/opt/.venv"

logger = logging.getLogger(__name__)

_DEFAULT_SETUP_PYTHON_PACKAGES = [
    "pytest",
    "numpy",
    "scipy",
    "pandas",
    "statsmodels",
    "scikit-learn",
    "requests",
    "pyyaml",
    "matplotlib",
    "pillow",
    "sympy",
]
_REQUIRED_SETUP_PYTHON_PACKAGES = [
    "pandas",
    "scipy",
]


def _build_linux_binary(build_target: Path) -> Path:
    """Build gollem Linux binary at the target path."""
    subprocess.run(
        ["go", "build", "-o", str(build_target), "./cmd/gollem/"],
        cwd=str(_REPO_ROOT),
        env={**os.environ, "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0"},
        check=True,
        capture_output=True,
    )
    if not build_target.exists():
        raise FileNotFoundError(f"go build succeeded but binary not found at {build_target}")
    return build_target


def _binary_is_stale(binary: Path) -> bool:
    """Return True when repo source appears newer than the binary."""
    if not binary.exists():
        return True
    try:
        binary_mtime = binary.stat().st_mtime
        newest_source_mtime = 0.0
        for rel in ("go.mod", "go.sum"):
            p = _REPO_ROOT / rel
            if p.exists():
                newest_source_mtime = max(newest_source_mtime, p.stat().st_mtime)
        for rel in ("cmd", "core", "ext", "modelutil", "provider"):
            root = _REPO_ROOT / rel
            if not root.exists():
                continue
            for go_file in root.rglob("*.go"):
                newest_source_mtime = max(newest_source_mtime, go_file.stat().st_mtime)
        return newest_source_mtime > binary_mtime
    except Exception:
        # If freshness check fails, rebuild to be safe.
        return True


def _find_binary() -> Path:
    """Locate the pre-built gollem Linux binary.

    Resolution order:
    1. GOLLEM_BINARY env var (explicit override)
    2. Auto-rebuild canonical harbor/gollem-linux-amd64 when stale
    3. Existing harbor/gollem-linux-amd64
    4. harbor/gollem_harbor/gollem (fallback artifact)
    5. Last-chance build from source tree
    """
    # Check env var.
    if env_path := os.environ.get("GOLLEM_BINARY"):
        p = Path(env_path)
        if p.exists():
            return p

    build_target = _PKG_DIR.parent / "gollem-linux-amd64"
    cmd_dir = _REPO_ROOT / "cmd" / "gollem"
    candidate2 = _PKG_DIR / "gollem"

    # Auto-rebuild when repo sources are available and newer than the binary.
    # This prevents stale binaries from silently running old agent logic.
    skip_rebuild = os.environ.get("GOLLEM_SKIP_REBUILD", "").strip().lower() in {"1", "true", "yes"}
    force_rebuild = os.environ.get("GOLLEM_FORCE_REBUILD", "").strip().lower() in {"1", "true", "yes"}
    if cmd_dir.exists() and shutil.which("go") and not skip_rebuild:
        try:
            if force_rebuild or _binary_is_stale(build_target):
                return _build_linux_binary(build_target)
            if build_target.exists():
                return build_target
        except Exception as e:
            logger.warning(f"Failed to rebuild gollem binary, falling back to existing artifacts: {e}")

    # Check canonical location next to pyproject.toml.
    if build_target.exists():
        return build_target

    # Check inside the package directory.
    if candidate2.exists():
        return candidate2

    # Last chance build (source tree present but stale check was skipped).
    if cmd_dir.exists() and shutil.which("go"):
        return _build_linux_binary(build_target)

    raise FileNotFoundError(
        "gollem Linux binary not found. Build it with:\n"
        "  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o harbor/gollem-linux-amd64 ./cmd/gollem/"
    )


class GollemAgent(BaseInstalledAgent):
    """Harbor agent that runs the gollem coding agent CLI.

    Instead of compiling Go inside the container (slow), we upload a
    pre-built static binary. This reduces setup time from 6+ minutes
    to under 10 seconds.
    """

    SUPPORTS_ATIF = False

    def __init__(
        self,
        *args,
        timeout_minutes: int = 210,
        thinking_budget: int = 0,
        reasoning_effort: str = "",
        location: str = "",
        project: str = "",
        **kwargs,
    ):
        super().__init__(*args, **kwargs)
        self._timeout_minutes = timeout_minutes
        self._thinking_budget = thinking_budget
        self._reasoning_effort = reasoning_effort
        self._location = location
        self._project = project
        self._setup_python_packages = self._resolve_setup_python_packages()

    @staticmethod
    def _resolve_setup_python_packages() -> list[str]:
        """Return setup-time Python prewarm packages from env or defaults."""
        def with_required(packages: list[str]) -> list[str]:
            # Always keep key scientific stack available for TB2 tasks.
            seen: set[str] = set()
            out: list[str] = []
            for pkg in [*packages, *_REQUIRED_SETUP_PYTHON_PACKAGES]:
                if pkg not in seen:
                    seen.add(pkg)
                    out.append(pkg)
            return out

        raw = os.environ.get("GOLLEM_SETUP_PYTHON_PACKAGES", "").strip()
        if raw:
            # Accept either comma or whitespace separated package names.
            tokens = [tok.strip() for tok in raw.replace(",", " ").split() if tok.strip()]
            # Deduplicate while preserving order.
            seen: set[str] = set()
            packages: list[str] = []
            for tok in tokens:
                if tok not in seen:
                    seen.add(tok)
                    packages.append(tok)
            if packages:
                return with_required(packages)
        return with_required(list(_DEFAULT_SETUP_PYTHON_PACKAGES))

    @staticmethod
    def name() -> str:
        return "gollem"

    @property
    def _install_agent_template_path(self) -> Path:
        return _TEMPLATES_DIR / "install.sh.j2"

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        """Override run() to detect the task-specific timeout before execution.

        Harbor wraps this in asyncio.wait_for(timeout=task_timeout_sec), but
        the agent binary doesn't know the real deadline. We read the task
        timeout from the task cache and pass it via GOLLEM_TIMEOUT_SEC.
        """
        task_timeout = self._detect_task_timeout()
        if task_timeout:
            self._task_timeout_sec = task_timeout
            logger.info(f"Detected task timeout: {task_timeout}s")
        else:
            self._task_timeout_sec = None

        # Fail fast on missing provider credentials/config to avoid wasting
        # benchmark attempts on immediate model initialization errors.
        self._validate_provider_auth()

        await super().run(instruction, environment, context)

    def _detect_task_timeout(self) -> int | None:
        """Find the task-specific timeout from Harbor's task cache.

        The trial directory name has format '<task-name>__<random>'.
        We extract the task name and search the task cache for task.toml.
        """
        try:
            # Extract task name from trial directory name.
            trial_dir = self.logs_dir.parent
            trial_name = trial_dir.name
            task_name = trial_name.rsplit("__", 1)[0]
            if not task_name:
                return None

            # Search the task cache for this task's task.toml.
            if _TASK_CACHE_DIR.exists():
                for cache_entry in _TASK_CACHE_DIR.iterdir():
                    task_toml = cache_entry / task_name / "task.toml"
                    if task_toml.exists():
                        return self._parse_timeout_from_toml(task_toml)
        except Exception as e:
            logger.debug(f"Failed to detect task timeout: {e}")
        return None

    @staticmethod
    def _task_timeout_buffer_sec() -> int:
        """Optional timeout buffer before Harbor's hard task deadline."""
        raw = os.environ.get("GOLLEM_TASK_TIMEOUT_BUFFER_SEC", "0").strip()
        try:
            val = int(raw)
        except ValueError:
            return 0
        return max(val, 0)

    @staticmethod
    def _parse_timeout_from_toml(path: Path) -> int | None:
        """Parse agent.timeout_sec from a task.toml file."""
        try:
            text = path.read_text()
            in_agent = False
            for line in text.splitlines():
                line = line.strip()
                if line == "[agent]":
                    in_agent = True
                elif line.startswith("["):
                    in_agent = False
                elif in_agent and line.startswith("timeout_sec"):
                    _, _, val = line.partition("=")
                    return int(float(val.strip()))
        except Exception:
            pass
        return None

    @staticmethod
    def _resolve_host_gcp_credentials() -> Path | None:
        """Return host ADC path if available."""
        creds_file = os.environ.get("GOOGLE_APPLICATION_CREDENTIALS", "")
        if creds_file:
            p = Path(creds_file)
            if p.exists():
                return p
        default_adc = Path.home() / ".config" / "gcloud" / "application_default_credentials.json"
        if default_adc.exists():
            return default_adc
        return None

    def _validate_provider_auth(self) -> None:
        """Validate provider auth/env before launching the agent command."""
        provider, _ = self._parse_model_name()

        if provider == "anthropic":
            if not os.environ.get("ANTHROPIC_API_KEY"):
                raise RuntimeError(
                    "Missing ANTHROPIC_API_KEY for anthropic model. "
                    "Set ANTHROPIC_API_KEY before running Harbor."
                )
            return

        if provider == "openai":
            if not os.environ.get("OPENAI_API_KEY"):
                raise RuntimeError(
                    "Missing OPENAI_API_KEY for openai model. "
                    "Set OPENAI_API_KEY before running Harbor."
                )
            return

        if provider in ("vertexai", "vertexai-anthropic"):
            project = self._project or os.environ.get("GOOGLE_CLOUD_PROJECT", "")
            if not project:
                raise RuntimeError(
                    "Missing GOOGLE_CLOUD_PROJECT for Vertex AI model. "
                    "Set GOOGLE_CLOUD_PROJECT or pass --ak project=<id>."
                )
            if self._resolve_host_gcp_credentials() is None:
                raise RuntimeError(
                    "Missing GCP Application Default Credentials. "
                    "Set GOOGLE_APPLICATION_CREDENTIALS or run "
                    "`gcloud auth application-default login`."
                )

    async def setup(self, environment: BaseEnvironment) -> None:
        """Upload the pre-built binary instead of compiling from source."""
        binary_path = _find_binary()
        setup_python_pkgs = " ".join(shlex.quote(pkg) for pkg in self._setup_python_packages)

        # Combined setup: fix dpkg, install CA certs and common tools, create
        # bin dir. Keep this intentionally lean because setup has its own
        # timeout budget, even though it is separate from agent execution time.
        await environment.exec(
            command=(
                "dpkg --configure -a > /dev/null 2>&1 || true; "
                "mkdir -p /usr/local/bin; "
                # Create a small swap file to reduce OOM risk without large setup
                # overhead under slower/emulated environments.
                "if [ ! -f /swapfile ]; then "
                "  (fallocate -l 256M /swapfile 2>/dev/null || "
                "   dd if=/dev/zero of=/swapfile bs=1M count=256 2>/dev/null) && "
                "  chmod 600 /swapfile && "
                "  mkswap /swapfile 2>/dev/null && "
                "  swapon /swapfile 2>/dev/null || true; "
                "fi; "
                "timeout 120 sh -c '("
                "  if command -v apt-get >/dev/null 2>&1; then "
                "    export DEBIAN_FRONTEND=noninteractive; "
                "    apt-get update -qq 2>/dev/null && "
                "    apt-get install -y -qq --no-install-recommends "
                "      ca-certificates python3 python3-venv build-essential "
                "      curl wget git jq unzip file bc sqlite3 xxd pkg-config "
                "      cmake libssl-dev libffi-dev zlib1g-dev "
                "      libsqlite3-dev libreadline-dev 2>/dev/null; "
                "    apt-get install -y -qq --no-install-recommends stockfish "
                "      2>/dev/null || true; "
                "  elif command -v apk >/dev/null 2>&1; then "
                "    apk add --no-cache ca-certificates python3 build-base "
                "      curl wget git jq unzip file bc sqlite cmake openssl-dev 2>/dev/null; "
                "  elif command -v yum >/dev/null 2>&1; then "
                "    yum install -y ca-certificates python3 gcc make "
                "      curl wget git jq unzip file bc sqlite cmake openssl-devel 2>/dev/null; "
                "  fi"
                ") 2>&1 | tail -5' || true; "
                "if [ -x /usr/games/stockfish ]; then "
                "  ln -sf /usr/games/stockfish /usr/local/bin/stockfish 2>/dev/null || true; "
                "fi; "
                "update-ca-certificates > /dev/null 2>&1 || true"
            )
        )

        # Upload binary to container and make executable.
        await environment.upload_file(
            source_path=binary_path,
            target_path="/usr/local/bin/gollem",
        )
        await environment.exec(command="chmod +x /usr/local/bin/gollem")

        # Verify.
        result = await environment.exec(command="gollem --help")
        if result.return_code != 0:
            raise RuntimeError(
                f"gollem binary verification failed: {result.stderr}"
            )

        # Upload GCP credentials file for Vertex AI authentication.
        # SECURITY: Only upload credentials to local Docker containers, never
        # to remote environments (Modal, E2B, GKE, etc.) where the file would
        # leave this machine.
        host_creds = self._resolve_host_gcp_credentials()
        if host_creds is not None:
            from harbor.environments.docker.docker import DockerEnvironment
            if isinstance(environment, DockerEnvironment):
                await environment.exec(
                    command="mkdir -p /root/.config/gcloud"
                )
                await environment.upload_file(
                    source_path=host_creds,
                    target_path="/root/.config/gcloud/application_default_credentials.json",
                )
                logger.info("Uploaded GCP credentials to local Docker container")
            else:
                logger.warning(
                    "Skipping GCP credentials upload: not a local Docker environment. "
                    "Vertex AI auth will not work in remote environments."
                )

        # Install uv (required for Python dependency setup). Fail fast if
        # unavailable so we don't silently fall back to slower pip paths.
        uv_install_result = await environment.exec(
            command=(
                "timeout 60 sh -c '"
                "set -e; "
                "if ! command -v uv >/dev/null 2>&1; then "
                "  ok=0; "
                "  for i in 1 2 3; do "
                "    if curl -LsSf https://astral.sh/uv/install.sh 2>/dev/null | sh 2>/dev/null; then "
                "      ok=1; break; "
                "    fi; "
                "    sleep $((i * 2)); "
                "  done; "
                "  if [ \"$ok\" -ne 1 ]; then exit 1; fi; "
                "fi; "
                "export PATH=\"$HOME/.local/bin:$PATH\"; "
                "if command -v uv >/dev/null 2>&1; then "
                "  ln -sf \"$(command -v uv)\" /usr/local/bin/uv 2>/dev/null || true; "
                "fi; "
                "command -v uv >/dev/null 2>&1"
                "'"
            )
        )
        if uv_install_result.return_code != 0:
            raise RuntimeError("setup failed: could not install/verify uv")

        # Create an isolated virtualenv so Python package installs do not
        # consume agent turns and do not mutate system Python.
        venv_result = await environment.exec(
            command=(
                "timeout 60 sh -c '"
                "set -e; "
                f"if [ ! -x {_AGENT_VENV_PATH}/bin/python ]; then "
                f"  uv venv {_AGENT_VENV_PATH} >/dev/null 2>&1; "
                "fi; "
                f"uv pip install --python {_AGENT_VENV_PATH}/bin/python -q "
                "  --upgrade pip setuptools wheel >/dev/null 2>&1"
                "'"
            )
        )
        if venv_result.return_code != 0:
            raise RuntimeError("setup failed: could not create Python venv with uv")

        # Auto-install common task dependencies with uv only.
        py_setup_result = await environment.exec(
            command=(
                "timeout 180 sh -c '"
                "set -e; "
                f"PY_INSTALL=\"uv pip install --python {_AGENT_VENV_PATH}/bin/python\"; "
                f"$PY_INSTALL -q {setup_python_pkgs} 2>/dev/null || true; "
                # Always keep pandas/scipy available even if caller overrides
                # GOLLEM_SETUP_PYTHON_PACKAGES.
                "$PY_INSTALL -q pandas scipy 2>/dev/null || true; "
                # Fail setup if required scientific deps are not importable.
                f"{_AGENT_VENV_PATH}/bin/python -c \"import pandas, scipy\" >/dev/null 2>&1; "
                # Python: requirements.txt
                "for f in /app/requirements.txt /requirements.txt; do "
                "  if [ -f \"$f\" ]; then "
                "    $PY_INSTALL -r \"$f\" 2>&1 | tail -5 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Python: setup.py (editable install for projects with setup.py)
                "for d in /app .; do "
                "  if [ -f \"$d/setup.py\" ] && ! [ -f \"$d/requirements.txt\" ]; then "
                "    cd \"$d\" && $PY_INSTALL -e . 2>&1 | tail -5 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Node.js: package.json
                "for d in /app .; do "
                "  if [ -f \"$d/package.json\" ] && command -v npm >/dev/null 2>&1; then "
                "    cd \"$d\" && npm install --no-audit --no-fund 2>&1 | tail -5 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Go: go.mod
                "for d in /app .; do "
                "  if [ -f \"$d/go.mod\" ] && command -v go >/dev/null 2>&1; then "
                "    cd \"$d\" && go mod download 2>&1 | tail -3 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Rust: Cargo.toml — pre-fetch dependencies.
                "for d in /app .; do "
                "  if [ -f \"$d/Cargo.toml\" ] && command -v cargo >/dev/null 2>&1; then "
                "    cd \"$d\" && cargo fetch 2>&1 | tail -3 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Python: pyproject.toml (PEP 517/518 projects without requirements.txt)
                "for d in /app .; do "
                "  if [ -f \"$d/pyproject.toml\" ] && ! [ -f \"$d/requirements.txt\" ] && ! [ -f \"$d/setup.py\" ]; then "
                "    cd \"$d\" && $PY_INSTALL -e . 2>&1 | tail -5 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Ruby: Gemfile — install gems
                "for d in /app .; do "
                "  if [ -f \"$d/Gemfile\" ] && command -v bundle >/dev/null 2>&1; then "
                "    cd \"$d\" && bundle install 2>&1 | tail -5 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Java: Maven pom.xml — download dependencies
                "for d in /app .; do "
                "  if [ -f \"$d/pom.xml\" ] && command -v mvn >/dev/null 2>&1; then "
                "    cd \"$d\" && mvn dependency:resolve -q 2>&1 | tail -5 || true; "
                "    break; "
                "  fi; "
                "done; "
                # Java: Gradle — download dependencies
                "for d in /app .; do "
                "  if [ -f \"$d/build.gradle\" ] || [ -f \"$d/build.gradle.kts\" ]; then "
                "    cd \"$d\"; "
                "    if [ -x \"$d/gradlew\" ]; then "
                "      ./gradlew dependencies --quiet 2>&1 | tail -5 || true; "
                "    elif command -v gradle >/dev/null 2>&1; then "
                "      gradle dependencies --quiet 2>&1 | tail -5 || true; "
                "    fi; "
                "    break; "
                "  fi; "
                "done"
                "'"
            )
        )
        if py_setup_result.return_code != 0:
            raise RuntimeError("setup failed: Python dependency prewarm with uv failed")

        # Always pre-install Python LSP via uv (pyright is the primary server).
        await environment.exec(
            command=(
                "timeout 60 sh -c '"
                f"uv pip install --python {_AGENT_VENV_PATH}/bin/python -q "
                "  pyright 2>/dev/null || true"
                "' || true"
            )
        )

        # Optional: pre-install additional non-Python language servers.
        install_lsp = os.environ.get("GOLLEM_SETUP_INSTALL_LSP", "").strip().lower() in {
            "1", "true", "yes"
        }
        if install_lsp:
            await environment.exec(
                command=(
                    "timeout 90 sh -c '"
                    "if command -v go >/dev/null 2>&1; then "
                    "  GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest 2>/dev/null || true; "
                    "fi; "
                    "if command -v npm >/dev/null 2>&1; then "
                    "  npm i -g typescript-language-server typescript bash-language-server 2>/dev/null || true; "
                    "fi"
                    "' || true"
                )
            )

            await environment.exec(
                command=(
                    "for ls in gopls pyright-langserver pylsp typescript-language-server "
                    "bash-language-server clangd; do "
                    "  if command -v \"$ls\" >/dev/null 2>&1; then echo \"[gollem] lsp-ready: $ls\"; fi; "
                    "done"
                )
            )
        else:
            logger.info(
                "Skipping extra LSP preinstall (set GOLLEM_SETUP_INSTALL_LSP=1 to enable go/js). "
                "Python LSP (pyright) is installed by default."
            )

    def create_run_agent_commands(self, instruction: str) -> list[ExecInput]:
        """Build the shell command to invoke gollem run inside the container."""
        provider, model = self._parse_model_name()

        # Use the task-specific timeout if detected, otherwise fall back
        # to the generic timeout_minutes.
        task_timeout = getattr(self, "_task_timeout_sec", None)
        if task_timeout:
            timeout_buffer = self._task_timeout_buffer_sec()
            if timeout_buffer >= task_timeout:
                timeout_buffer = 0
            agent_timeout_secs = max(task_timeout - timeout_buffer, 1)
            gollem_timeout_sec = task_timeout
            exec_timeout_sec = task_timeout + 120
        else:
            agent_timeout_secs = max((self._timeout_minutes - 1) * 60, 60)
            gollem_timeout_sec = self._timeout_minutes * 60
            exec_timeout_sec = self._timeout_minutes * 60 + 60

        cmd_parts = [
            "/usr/local/bin/gollem", "run",
            "--provider", provider,
        ]
        if model:
            cmd_parts.extend(["--model", model])
        cmd_parts.extend([
            "--timeout", f"{agent_timeout_secs}s",
        ])
        if self._thinking_budget > 0:
            cmd_parts.extend(["--thinking-budget", str(self._thinking_budget)])
        if self._reasoning_effort:
            cmd_parts.extend(["--reasoning-effort", self._reasoning_effort])
        if self._location:
            cmd_parts.extend(["--location", self._location])
        if self._project:
            cmd_parts.extend(["--project", self._project])

        # Harbor-specific objective framing: optimize for verifier score on
        # Terminal-Bench, not just self-reported completion.
        competition_prompt_enabled = os.environ.get(
            "GOLLEM_TBENCH_COMPETITION_PROMPT", "1"
        ).strip().lower() in {"1", "true", "yes", "on"}
        run_instruction = instruction
        if competition_prompt_enabled:
            run_instruction = (
                "TERMINAL-BENCH 2.0 COMPETITION MODE:\n"
                "- Only verifier results determine success.\n"
                "- Prioritize passing verifier checks over self-reported completion.\n"
                "- Read verifier tests/scripts early and validate against them before finishing.\n"
                "- Optimize for both correctness and runtime under timeout.\n"
                "- For service tasks, do NOT use broad kill commands (pkill -f, killall). "
                "Use PID files and exact PID-based stop/start.\n"
                "- For service tasks, verify readiness before finishing (port listening + "
                "real protocol request, not just process existence).\n"
                "- If requirements specify exact output files/dir contents, remove intermediate "
                "artifacts before finalizing.\n\n"
                f"{instruction}"
            )

        cmd_parts.append(shlex.quote(run_instruction))

        # Tee stderr to a log file for real-time observability.
        # Use: docker exec <container> tail -f /tmp/gollem.log
        # stdbuf -oL forces line buffering so logs appear in real-time.
        raw_cmd = " ".join(cmd_parts)
        cmd_with_logging = f"{{ {raw_cmd} ; }} 2>&1 | stdbuf -oL tee /tmp/gollem.log"

        env = {
            "PATH": f"{_AGENT_VENV_PATH}/bin:/root/.local/bin:/root/go/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin",
            "HOME": "/root",
            "VIRTUAL_ENV": _AGENT_VENV_PATH,
            # Common CA cert bundle paths for TLS verification.
            "SSL_CERT_FILE": "/etc/ssl/certs/ca-certificates.crt",
            "SSL_CERT_DIR": "/etc/ssl/certs",
            # Pass the REAL task timeout to gollem so TimeBudgetMiddleware
            # warns at the correct percentages of remaining time.
            "GOLLEM_TIMEOUT_SEC": str(gollem_timeout_sec),
            # Default to single-agent execution on Harbor to avoid shared-throughput
            # contention. Can be overridden by explicitly setting GOLLEM_TEAM_MODE.
            "GOLLEM_TEAM_MODE": os.environ.get("GOLLEM_TEAM_MODE", "off"),
            # Runtime dependency installs waste model budget; do them in setup.
            "GOLLEM_DISABLE_RUNTIME_DEP_INSTALL": os.environ.get(
                "GOLLEM_DISABLE_RUNTIME_DEP_INSTALL", "1"
            ),
            # Inform the agent which Python packages are prewarmed.
            "GOLLEM_PREINSTALLED_PYTHON_PACKAGES": " ".join(self._setup_python_packages),
            # Prevent __pycache__ directories — they cause "extra files" failures
            # when tests check directory contents with os.listdir/ls.
            "PYTHONDONTWRITEBYTECODE": "1",
            # Ensure Python output is immediately visible (not buffered). Critical
            # for real-time monitoring and for capturing output from crashed scripts.
            "PYTHONUNBUFFERED": "1",
            # Consistent UTF-8 locale for text processing tasks.
            "LANG": "C.UTF-8",
            "LC_ALL": "C.UTF-8",
        }

        for key in [
            "ANTHROPIC_API_KEY",
            "OPENAI_API_KEY",
            "OPENAI_BASE_URL",
            "OPENAI_PROMPT_CACHE_KEY",
            "OPENAI_PROMPT_CACHE_RETENTION",
            "OPENAI_SERVICE_TIER",
            "VERTEXAI_ANTHROPIC_PROMPT_CACHE",
            "VERTEXAI_ANTHROPIC_PROMPT_CACHE_TTL",
            "GOLLEM_MODEL_REQUEST_TIMEOUT_SEC",
            "GOOGLE_CLOUD_PROJECT",
            "LANGFUSE_SECRET_KEY",
            "LANGFUSE_PUBLIC_KEY",
            "LANGFUSE_BASE_URL",
        ]:
            if val := os.environ.get(key):
                env[key] = val

        if provider == "openai":
            env.setdefault("OPENAI_SERVICE_TIER", "priority")

        # Point to the uploaded credentials file inside the container.
        # Set this whenever we have GCP credentials (explicit or default ADC).
        provider, _ = self._parse_model_name()
        if provider in ("vertexai", "vertexai-anthropic"):
            if self._resolve_host_gcp_credentials() is not None:
                env["GOOGLE_APPLICATION_CREDENTIALS"] = (
                    "/root/.config/gcloud/application_default_credentials.json"
                )

        return [
            ExecInput(
                command=cmd_with_logging,
                env=env,
                timeout_sec=exec_timeout_sec,
            ),
        ]

    def populate_context_post_run(self, context: AgentContext) -> None:
        """Parse gollem output from the command logs."""
        import re

        command_dir = self.logs_dir / "command-0"
        if not command_dir.exists():
            return

        stdout_path = command_dir / "stdout.txt"
        stderr_path = command_dir / "stderr.txt"
        return_code_path = command_dir / "return-code.txt"

        stdout = stdout_path.read_text() if stdout_path.exists() else ""
        stderr = stderr_path.read_text() if stderr_path.exists() else ""
        return_code = (
            int(return_code_path.read_text().strip())
            if return_code_path.exists()
            else -1
        )

        # Harbor's Docker exec merges stderr into stdout, so stderr.txt
        # may not exist or may be empty. Search both for log data.
        combined_output = stdout + "\n" + stderr

        trajectory = {
            "agent": "gollem",
            "model": self.model_name,
            "return_code": return_code,
            "stdout": stdout[-5000:] if len(stdout) > 5000 else stdout,
            "stderr": stderr[-5000:] if len(stderr) > 5000 else stderr,
        }

        # Parse token usage from gollem's "done" line.
        # Formats:
        #   "gollem: done (tokens: 12345 in, 6789 out, tools: 42)"
        #   "gollem: done (tokens: 12345 in, 6789 out, cache_read: 111, tools: 42)"
        # Search combined output since stderr may be merged into stdout.
        done_matches = re.findall(
            r"^gollem:\s+done\s+\(tokens:\s*(\d+)\s*in,\s*(\d+)\s*out(?:,\s*cache_read:\s*(\d+))?,\s*tools:\s*(\d+)\)",
            combined_output,
            flags=re.MULTILINE,
        )
        if done_matches:
            input_tokens, output_tokens, cache_read_tokens, tool_calls = done_matches[-1]
            trajectory["input_tokens"] = int(input_tokens)
            trajectory["output_tokens"] = int(output_tokens)
            if cache_read_tokens:
                trajectory["cache_read_tokens"] = int(cache_read_tokens)
            trajectory["tool_calls"] = int(tool_calls)

        # Count tool invocations from log hooks.
        # Split top-level tool calls from nested execute_code calls:
        #   [gollem] tool:start <tool>
        #   [gollem] tool:start inner:<tool>
        top_level_tool_starts = len(
            re.findall(
                r"^\[gollem\] tool:start (?!inner:)",
                combined_output,
                flags=re.MULTILINE,
            )
        )
        inner_tool_starts = len(
            re.findall(
                r"^\[gollem\] tool:start inner:",
                combined_output,
                flags=re.MULTILINE,
            )
        )
        total_tool_starts = top_level_tool_starts + inner_tool_starts
        if total_tool_starts > 0:
            # Backward-compatible field: top-level tool calls only.
            trajectory["tool_invocations"] = top_level_tool_starts
            # Explicit split fields for analysis.
            trajectory["tool_invocations_top_level"] = top_level_tool_starts
            trajectory["tool_invocations_inner"] = inner_tool_starts
            trajectory["tool_invocations_total"] = total_tool_starts

        traj_path = self.logs_dir / "trajectory.json"
        traj_path.write_text(json.dumps(trajectory, indent=2))

        # Populate AgentContext with token usage.
        if "input_tokens" in trajectory:
            context.n_input_tokens = trajectory["input_tokens"]
        if "output_tokens" in trajectory:
            context.n_output_tokens = trajectory["output_tokens"]
        if "cache_read_tokens" in trajectory:
            context.n_cache_tokens = trajectory["cache_read_tokens"]

        context.metadata = {
            "trajectory_path": str(traj_path),
            "return_code": return_code,
            "tool_invocations": trajectory.get("tool_invocations", 0),
            "tool_invocations_top_level": trajectory.get("tool_invocations_top_level", 0),
            "tool_invocations_inner": trajectory.get("tool_invocations_inner", 0),
            "tool_invocations_total": trajectory.get("tool_invocations_total", 0),
        }

    def _parse_model_name(self) -> tuple[str, str]:
        """Parse 'provider/model' format into (provider, model) tuple."""
        if not self.model_name:
            return "anthropic", ""

        if "/" in self.model_name:
            provider, model = self.model_name.split("/", 1)
            # Harbor may append routing suffixes like "@default" to model IDs.
            # Provider APIs expect the raw model name without this suffix.
            if "@" in model:
                model = model.split("@", 1)[0]
            provider_map = {
                "anthropic": "anthropic",
                "openai": "openai",
                "google": "vertexai",
                "vertexai": "vertexai",
                "vertex": "vertexai",
            }
            return provider_map.get(provider, provider), model

        return "anthropic", self.model_name
