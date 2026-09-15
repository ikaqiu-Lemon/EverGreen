# Contributing to Evergreen

Thank you for helping improve Evergreen.

## Development setup

Evergreen requires Go 1.25 or newer, Git, GNU Make, Python 3, and the Python
packages listed in `requirements-dev.txt`.

```bash
python3 -m venv .venv
. .venv/bin/activate
python3 -m pip install -r requirements-dev.txt
go mod download
```

Run the standard checks before submitting a change:

```bash
make lint
make test
```

Use `make test-full` for changes that affect shared behavior, storage,
transactions, indexing, or release packaging. The full profile can take
substantially longer than the core profile.

## Engineering rules

- Keep Markdown and frontmatter as the authoritative data source.
- Do not serialize and rewrite complete YAML documents. Preserve unknown fields
  and user-authored bytes through the existing span-based write APIs.
- Keep product packages independent from test-only packages.
- Put Go tests under `tests/_staged/` and register every suite in
  `tests/manifest/suites.yaml`.
- Add or update tests whenever observable CLI behavior changes.
- Keep changes focused. Avoid unrelated formatting or refactoring.

See `docs/ARCHITECTURE.md` for the package boundaries and safety model.

## Pull requests

1. Open an issue first for behavior changes or broad refactors.
2. Use a short, imperative commit subject.
3. Explain user-visible behavior, compatibility impact, and verification.
4. Keep generated files and local build outputs out of the commit.
5. Confirm that `git status --short` is clean after the test suite finishes.

By contributing, you agree that your contributions are provided under the
license in `LICENSE`.
