import importlib.util
import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
GENERATOR_PATH = ROOT / "scripts" / "generate-third-party-notices.py"
SPEC = importlib.util.spec_from_file_location("third_party_notices", GENERATOR_PATH)
GENERATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GENERATOR)


class ThirdPartyNoticeTest(unittest.TestCase):
    def test_electron_and_evergreen_versions_follow_manifests(self):
        package = json.loads((ROOT / "app" / "package.json").read_text(encoding="utf-8"))
        electron_version = package["devDependencies"]["electron"]
        electron = next(component for component in GENERATOR.BUNDLED_COMPONENTS if component[0] == "Electron")
        self.assertEqual(electron_version, electron[1])

        go_mod = (ROOT / "kernel" / "go.mod").read_text(encoding="utf-8")
        match = re.search(r"github\.com/ikaqiu-Lemon/EverGreen\s+(\S+)", go_mod)
        self.assertIsNotNone(match)
        evergreen_version = match.group(1)

        notices = (ROOT / "THIRD_PARTY_NOTICES.md").read_text(encoding="utf-8")
        self.assertIn(f"| Electron | {electron_version} |", notices)
        self.assertIn(f"### Electron {electron_version} - LICENSE", notices)
        self.assertIn(
            f"| `github.com/ikaqiu-Lemon/EverGreen` | {evergreen_version} | Apache-2.0 |",
            notices,
        )
        self.assertIn(
            f"### github.com/ikaqiu-Lemon/EverGreen {evergreen_version} - LICENSE",
            notices,
        )

    def test_corresponding_source_offer_follows_release_version_and_all_packages(self):
        version = json.loads((ROOT / "app" / "package.json").read_text(encoding="utf-8"))["version"]
        offer = (ROOT / "SOURCE_CODE.md").read_text(encoding="utf-8")
        self.assertIn(f"<!-- release-version: {version} -->", offer)
        self.assertIn(
            f"https://github.com/siyuan-note/siyuan/releases/download/v{version}/"
            f"siyuan-{version}-source.tar.gz",
            offer,
        )
        self.assertIn(f"https://github.com/siyuan-note/siyuan/tree/v{version}", offer)

        configs = (
            "electron-builder.yml",
            "electron-builder-arm64.yml",
            "electron-builder-linux.yml",
            "electron-builder-linux-arm64.yml",
            "electron-builder-darwin.yml",
            "electron-builder-darwin-arm64.yml",
        )
        for filename in configs:
            with self.subTest(config=filename):
                config = (ROOT / "app" / filename).read_text(encoding="utf-8")
                self.assertIn('from: "../SOURCE_CODE.md"', config)
                self.assertIn("SOURCE_CODE.md", config)

        workflow = (ROOT / ".github" / "workflows" / "cd.yml").read_text(encoding="utf-8")
        self.assertIn('../SOURCE_CODE.md', workflow)
        self.assertIn('siyuan-${VERSION}-source.tar.gz', workflow)
        self.assertIn('git -C source-checkout archive', workflow)


if __name__ == "__main__":
    unittest.main()
