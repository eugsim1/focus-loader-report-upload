import unittest
from pathlib import Path

try:
    from streamlit.testing.v1 import AppTest
except ModuleNotFoundError:
    AppTest = None


@unittest.skipIf(AppTest is None, "Streamlit is not installed")
class StreamlitAppSmokeTests(unittest.TestCase):
    def test_app_renders_safe_backend_error(self):
        app_path = Path(__file__).resolve().parents[1] / "app.py"
        app = AppTest.from_file(str(app_path), default_timeout=10).run()
        self.assertEqual(len(app.exception), 0)
        self.assertEqual(app.title[0].value, "OCI FOCUS Loader")
        self.assertGreaterEqual(len(app.error), 1)


if __name__ == "__main__":
    unittest.main()
