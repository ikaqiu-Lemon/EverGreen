# Privacy

Evergreen is a local command-line application.

- It does not send telemetry.
- It does not call remote models or analytics services.
- It does not fetch URLs supplied to `eg capture`.
- It stores vault content only in the filesystem selected by the user.
- It invokes the local Git executable to version vault changes.

URLs, titles, source text, and other values supplied to the CLI may be written
to the selected vault and committed to that vault's Git history. Users are
responsible for choosing what data to store and where that repository is
published or synchronized.

The project CI downloads declared development dependencies when its cache is
empty. Released `eg` binaries do not require a network connection at runtime.
