"""Image-only contracts; no registry, Docker daemon or serving state."""
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("image_matrix", Path(__file__).parents[1] / "image_matrix.py")
MATRIX = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MATRIX)


class ImageMatrixTest(unittest.TestCase):
    def test_actual_runtime_helpers_are_required_and_cannot_hide_behind_profiles(self):
        names = ["lantern-0", "lantern-1", "lantern-2", "admin", "lantern-mcp", "metrics-0", "metrics-1", "metrics-2", "membershippublisher", "edge"]
        rendered = {"services": {name: {"image": "sha256:" + "b" * 64} for name in names}}
        self.assertEqual(set(MATRIX.selected_images(rendered, "oidc")), set(names))
        rendered["services"]["edge"]["profiles"] = ["optional"]
        with self.assertRaises(ValueError):
            MATRIX.selected_images(rendered, "oidc")
        del rendered["services"]["edge"]
        self.assertEqual(len(MATRIX.selected_images(rendered, "off")), 8)
        del rendered["services"]["metrics-2"]
        with self.assertRaises(ValueError):
            MATRIX.selected_images(rendered, "off")
    def test_exact_source_and_immutable_identity_are_required(self):
        source, image_id = "a" * 40, "sha256:" + "b" * 64
        def image(_):
            return [{"Id": image_id, "Config": {"Labels": {"org.opencontainers.image.revision": source}}}]
        result = MATRIX.inspect_images(source, {"server": image_id}, image)
        self.assertEqual(result["images"]["server"]["kind"], "local-source-build")
        with self.assertRaises(ValueError):
            MATRIX.inspect_images("c" * 40, {"server": image_id}, image)
        with self.assertRaises(ValueError):
            MATRIX.inspect_images(source, {"server": "lantern:latest"}, image)
        with self.assertRaises(ValueError):
            MATRIX.inspect_images(source, {"server": image_id}, lambda _: [])


if __name__ == "__main__":
    unittest.main()
