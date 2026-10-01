"""Azure Blob helper for backup.sh (audit AR-D04).

  python blob.py upload <local file> <blob name>
  python blob.py prune <retention days>
  python blob.py latest <prefix>      newest blob name under prefix (restore drill)
  python blob.py cat <blob name>      stream a blob to stdout (restore drill)

Env: AZURE_STORAGE_ACCOUNT_NAME, AZURE_STORAGE_ACCOUNT_KEY, BACKUP_CONTAINER.
"""

import os
import sys
from datetime import datetime, timedelta, timezone


def container_client():
    from azure.storage.blob import BlobServiceClient

    acct = os.environ["AZURE_STORAGE_ACCOUNT_NAME"]
    svc = BlobServiceClient(
        account_url=f"https://{acct}.blob.core.windows.net",
        credential=os.environ["AZURE_STORAGE_ACCOUNT_KEY"],
    )
    return svc.get_container_client(os.environ["BACKUP_CONTAINER"])


def upload(cc, path, name):
    # Never overwrite: names carry a timestamp, and an overwrite would only
    # hide a clock or naming bug behind a silently replaced backup.
    with open(path, "rb") as f:
        cc.upload_blob(name=name, data=f, overwrite=False)
    print(f"Uploaded {name} ({os.path.getsize(path) // (1024 * 1024)} MB)")


def prune(cc, now, days):
    """Delete backups older than `days`. Returns (deleted, failed).

    A failed delete (e.g. still under the container's immutability period) is
    logged and skipped: pruning must never fail a backup that has already
    been uploaded.
    """
    cutoff = now - timedelta(days=days)
    deleted = failed = 0
    for blob in cc.list_blobs():
        if blob.last_modified >= cutoff:
            continue
        try:
            cc.delete_blob(blob.name)
            deleted += 1
            print(f"Deleted old backup: {blob.name}")
        except Exception as e:  # noqa: BLE001
            failed += 1
            print(f"Could not delete {blob.name}: {type(e).__name__}: {e}")
    print(f"Prune done: {deleted} deleted, {failed} skipped (cutoff {cutoff:%Y-%m-%d %H:%M} UTC).")
    return deleted, failed


def latest(cc, prefix):
    """Name of the most recently modified blob under prefix, or None."""
    blobs = list(cc.list_blobs(name_starts_with=prefix))
    if not blobs:
        return None
    return max(blobs, key=lambda b: b.last_modified).name


def main(argv):
    if len(argv) == 4 and argv[1] == "upload":
        upload(container_client(), argv[2], argv[3])
    elif len(argv) == 3 and argv[1] == "prune":
        prune(container_client(), datetime.now(timezone.utc), int(argv[2]))
    elif len(argv) == 3 and argv[1] == "latest":
        name = latest(container_client(), argv[2])
        if not name:
            print(f"no blob under {argv[2]}", file=sys.stderr)
            return 1
        print(name)
    elif len(argv) == 3 and argv[1] == "cat":
        container_client().download_blob(argv[2]).readinto(sys.stdout.buffer)
    else:
        print(__doc__)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
