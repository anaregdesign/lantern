"""Private fixture bridge from required operator JSON to the product process."""

from pathlib import PurePosixPath


def startup_command(startup_file, *, initialize=False, binary="/server"):
    """Read current input inside the container; never record its secret bytes.

    Initialization is an explicit, separate process. Serving always uses the
    product's hard resume path and reads the operator input again on every run.
    Missing input terminates before either product command can execute.
    """
    for path in (startup_file, binary):
        parsed = PurePosixPath(path)
        if not parsed.is_absolute() or ".." in parsed.parts or "\x00" in str(path):
            raise ValueError("absolute clean fixture startup and binary paths required")
    return ["-ec", '\n'.join((
        'LANTERN_SECURITY_CONFIG_JSON="$(cat "$1")"',
        'export LANTERN_SECURITY_CONFIG_JSON',
        'if [ "$3" = initialize ]; then',
        '  exec "$2" init-security',
        'fi',
        'exec "$2"',
    )), "lantern-security-startup", str(startup_file), str(binary),
        "initialize" if initialize else "serve"]
