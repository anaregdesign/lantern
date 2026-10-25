"""Run the example's network-free tests in the existing required CI discovery."""
import importlib.util
from pathlib import Path
import unittest


EXAMPLE = Path(__file__).resolve().parents[2] / "deploy/compose/oidc-ha"


def load_tests(loader, standard_tests, pattern):
    suite = unittest.TestSuite()
    for name in ("prepare", "check"):
        # Explicit unique module names avoid collisions with other discovered
        # test_check/test_prepare modules. A fresh loader keeps the enclosing
        # .github/scripts discovery's top-level directory unchanged.
        spec = importlib.util.spec_from_file_location(
            f"_lantern_oidc_ha_example_test_{name}", EXAMPLE / f"test_{name}.py"
        )
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        suite.addTests(unittest.TestLoader().loadTestsFromModule(module))
    return suite
