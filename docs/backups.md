# Backups

Photostead copies its database every night at 3:30 (the server's time). The database holds
everything Photostead knows that isn't in your photo and video files: albums, people and their
names, faces, sharing, share links and personal links, descriptions and keywords written in
Photostead, favorites, ratings, the vault, settings and accounts. Your photos and videos aren't in
it (they stay in their folders), and thumbnails and previews can be made again.

- **Where.** `PHOTOSTEAD_BACKUP_DIR`, by default `backups/database` in the managed share, where
  the move writes. On Unraid that's `/mnt/user0/photostead/backups/database`: on the array, away
  from the cache pool the database lives on. `PHOTOSTEAD_BACKUP_DIR=off` turns backups off.
- **How many.** The newest 14 (`PHOTOSTEAD_BACKUP_KEEP`). For a library of about 220,000 files a copy
  is about 550 MB and takes about 20 seconds (pg_dump, custom format, zstd); 14 take about 8 GB.
- **Checked.** Each copy is read back in full before it counts. In the app, **Backups** (in the
  rail, for the admin) shows the last copy, the ones kept, and **Back up now**. A failed backup,
  or none for a day and a half, turns the status line at the bottom of the rail red.

- **Sidecars.** What you add is also written to XMP sidecars: beside the originals in the managed
  share (so they're backed up with your photos), and for read-only libraries in the data folder's
  `sidecars/`. Include that folder if you back up `/data` (the database copies already hold the same
  facts).
- **Its file list.** Beside each copy, `<copy>.files.tsv.gz` lists every file Photostead knows,
  with its library, path, size, time and SHA-256: after a disk loss, what to bring back and how to check
  it came back whole. It's kept and pruned with its copy.

## Checking the files themselves

Every night at 4:15, for up to an hour, Photostead reads files again and checks them against the
hash taken when they came in, the longest unchecked first, so the whole library is covered over a
week or so without keeping the disks busy. A file that looks the same (its size and time) but no
longer matches is **damaged**: it's listed under **Backups**, and the status line turns red. A file
whose size or time changed was changed on purpose (the rescan reads it again), and a missing one is
the rescan's to notice; neither is called damaged. **Check now** beside the files, under Backups, checks for an
hour straight away.

A damaged file is one to bring back from your own backup of the photos (Photostead doesn't copy
them); the file list beside the database copies says what it should be.

## Proving a copy restores

This restores a copy into a scratch database, compares every table's rows with the live database,
and drops the scratch database. The live database isn't touched.

```bash
docker exec photostead photostead backup check
```

Add `-copy photostead-2026-10-22-0330.dump` to check a particular copy (`photostead backup list`
shows them). The live database may have more rows than the copy: whatever was written since. It takes about a
minute (tested 2026-10-22: 64 tables, 1.67 million rows).

## Bringing a copy back

Photostead has to be stopped, because the restore needs the database to itself. On Unraid:

```bash
docker stop photostead
docker run --rm -v /mnt/user/appdata/photostead:/data -v /mnt/user0/photostead:/photos-array -e PHOTOSTEAD_MOVE_TARGET=/photos-array photostead:dev backup restore -copy photostead-2026-10-22-0330.dump
docker start photostead
```

The database there before isn't dropped. It's renamed `photostead_before_restore_<date>`, and the
restore prints how to drop it once all is well. If the whole appdata folder was lost, the same
command works on an empty one: the container makes a fresh database first. (Tested 2026-10-22 that
way, in a throwaway container with an empty folder: restored in about a minute, and Photostead
started on it.)
