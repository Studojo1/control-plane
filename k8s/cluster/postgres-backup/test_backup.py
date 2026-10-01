"""Regression guard for the Postgres backup job (audit AR-D04).

Run: python3 -m unittest discover -s k8s/cluster/postgres-backup
"""

import os
import re
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from types import SimpleNamespace

import blob

HERE = os.path.dirname(os.path.abspath(__file__))
NOW = datetime(2026, 10, 1, 2, 0, tzinfo=timezone.utc)


class FakeContainer:
    def __init__(self, blobs, undeletable=()):
        self.blobs = dict(blobs)
        self.undeletable = set(undeletable)
        self.uploaded = {}

    def list_blobs(self, name_starts_with=""):
        return [SimpleNamespace(name=n, last_modified=t) for n, t in self.blobs.items()
                if n.startswith(name_starts_with)]

    def delete_blob(self, name):
        if name in self.undeletable:
            raise RuntimeError("BlobImmutableDueToPolicy")
        del self.blobs[name]

    def upload_blob(self, name, data, overwrite):
        if not overwrite and (name in self.blobs or name in self.uploaded):
            raise RuntimeError("BlobAlreadyExists")
        self.uploaded[name] = data.read()


class PruneTest(unittest.TestCase):
    def test_deletes_only_blobs_older_than_retention(self):
        cc = FakeContainer({
            "postgres/postgres-old.sql.gz": NOW - timedelta(days=31),
            "careercoach/careercoach-new.sql.gz": NOW - timedelta(days=29),
            "postgres-backup-legacy.sql.gz": NOW - timedelta(days=40),
        })
        self.assertEqual(blob.prune(cc, NOW, 30), (2, 0))
        self.assertEqual(list(cc.blobs), ["careercoach/careercoach-new.sql.gz"])

    def test_undeletable_blob_does_not_fail_the_run(self):
        cc = FakeContainer(
            {"a.sql.gz": NOW - timedelta(days=31), "b.sql.gz": NOW - timedelta(days=31)},
            undeletable={"a.sql.gz"},
        )
        self.assertEqual(blob.prune(cc, NOW, 30), (1, 1))
        self.assertIn("a.sql.gz", cc.blobs)


class LatestTest(unittest.TestCase):
    def test_newest_under_prefix(self):
        cc = FakeContainer({
            "careercoach/careercoach-1.sql.gz": NOW - timedelta(days=2),
            "careercoach/careercoach-2.sql.gz": NOW - timedelta(days=1),
            "careercoach_staging/careercoach_staging-3.sql.gz": NOW,
        })
        self.assertEqual(blob.latest(cc, "careercoach/"), "careercoach/careercoach-2.sql.gz")
        self.assertIsNone(blob.latest(cc, "plane/"))


class UploadTest(unittest.TestCase):
    def test_never_overwrites(self):
        cc = FakeContainer({"x/x-1.sql.gz": NOW})
        with tempfile.NamedTemporaryFile() as f:
            f.write(b"dump")
            f.flush()
            blob.upload(cc, f.name, "y/y-1.sql.gz")
            self.assertEqual(cc.uploaded["y/y-1.sql.gz"], b"dump")
            with self.assertRaises(RuntimeError):
                blob.upload(cc, f.name, "x/x-1.sql.gz")


class ScriptTest(unittest.TestCase):
    def setUp(self):
        with open(os.path.join(HERE, "backup.sh")) as f:
            self.script = f.read()

    def test_bash_syntax(self):
        for script in ("backup.sh", "restore-drill.sh"):
            subprocess.run(["bash", "-n", os.path.join(HERE, script)], check=True)

    def test_dumps_every_database_not_one(self):
        # The pre-AR-D04 job dumped only $POSTGRES_DB ("postgres").
        self.assertIn("from pg_database where datallowconn and not datistemplate", self.script)
        self.assertNotIn("POSTGRES_DB", self.script)
        self.assertIn('for db in "${DBS[@]}"', self.script)

    def test_cronjobs_use_pg16_and_the_shared_script(self):
        with open(os.path.join(HERE, "cronjobs.yaml")) as f:
            manifest = f.read()
        images = re.findall(r"image:\s*(\S+)", manifest)
        self.assertEqual(len(images), 3)
        for image in images:
            self.assertTrue(image.startswith("postgres:16"), image)
        self.assertEqual(manifest.count("bash /scripts/backup.sh"), 3)
        self.assertEqual(manifest.count("concurrencyPolicy: Forbid"), 3)


if __name__ == "__main__":
    unittest.main()
