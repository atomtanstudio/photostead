#!/usr/bin/env python3
"""Makes Mac programs self-contained: copies the libraries they use from outside macOS (Homebrew's,
a build folder's) into a lib folder beside them, and points every reference at the copy, relative to
where each file is (@rpath, with an @loader_path rpath), so the folder works wherever it's moved.

    relink.py LIBDIR FILE...     the files' outside libraries go to LIBDIR (and theirs, and so on)

Only /usr/lib and /System are left as they are. Files are re-signed (ad hoc) afterwards by the
caller: changing a load command breaks a signature, and Apple silicon won't run an unsigned file."""

import os
import shutil
import subprocess
import sys

SYSTEM = ("/usr/lib/", "/System/")


def deps(path):
    out = subprocess.run(["otool", "-L", path], capture_output=True, text=True, check=True).stdout
    lines = out.splitlines()[1:]
    return [line.strip().split(" (compatibility")[0] for line in lines if line.strip()]


def own_id(path):
    out = subprocess.run(["otool", "-D", path], capture_output=True, text=True).stdout.splitlines()
    return out[1].strip() if len(out) > 1 else None


def rpaths(path):
    out = subprocess.run(["otool", "-l", path], capture_output=True, text=True).stdout.splitlines()
    found = []
    for i, line in enumerate(out):
        if "cmd LC_RPATH" in line:
            for j in range(i + 1, min(i + 4, len(out))):
                if out[j].strip().startswith("path "):
                    found.append(out[j].strip().split(" ", 1)[1].rsplit(" (offset", 1)[0])
    return found


def resolve(ref, origin):
    """The file an outside reference means (following @rpath and @loader_path in Homebrew's own,
    from where the file came from), else a library of that name in Homebrew's lib."""
    if ref.startswith("@loader_path/"):
        return os.path.realpath(os.path.join(os.path.dirname(origin), ref[len("@loader_path/"):]))
    if ref.startswith("@rpath/"):
        for rp in rpaths(origin):
            base = rp.replace("@loader_path", os.path.dirname(origin))
            cand = os.path.join(base, ref[len("@rpath/"):])
            if os.path.exists(cand):
                return os.path.realpath(cand)
        for lib in ("/opt/homebrew/lib", "/usr/local/lib"):
            cand = os.path.join(lib, ref[len("@rpath/"):])
            if os.path.exists(cand):
                return os.path.realpath(cand)
        return None
    return os.path.realpath(ref) if os.path.exists(ref) else None


def main():
    libdir = os.path.abspath(sys.argv[1])
    os.makedirs(libdir, exist_ok=True)
    todo = [os.path.abspath(f) for f in sys.argv[2:]]
    done = set()
    came_from = {}  # a copy → the file it was copied from (its references are relative to that)
    while todo:
        f = todo.pop()
        if f in done:
            continue
        done.add(f)
        changes = []
        for ref in deps(f):
            if ref.startswith(SYSTEM) or ref == own_id(f):
                continue
            if ref.startswith("@rpath/") and os.path.exists(os.path.join(libdir, os.path.basename(ref))):
                continue  # already ours
            src = resolve(ref, came_from.get(f, f))
            if src is None:
                if ref.startswith("@"):
                    continue
                sys.exit(f"relink: {f} needs {ref}, which isn't there")
            name = os.path.basename(ref)
            dst = os.path.join(libdir, name)
            if not os.path.exists(dst):
                shutil.copy2(src, dst)
                came_from[dst] = src
                os.chmod(dst, 0o755)
                subprocess.run(["install_name_tool", "-id", "@rpath/" + name, dst], check=True,
                               stderr=subprocess.DEVNULL)
                todo.append(dst)
            changes += ["-change", ref, "@rpath/" + name]
        me = own_id(f)
        if me and me.startswith("/") and not me.startswith(SYSTEM):
            changes += ["-id", "@rpath/" + os.path.basename(me)]  # a library's own name, not its build folder
        rel = os.path.relpath(libdir, os.path.dirname(f))
        want = "@loader_path" if rel == "." else "@loader_path/" + rel
        have = rpaths(f)
        cmd = ["install_name_tool", *changes]
        for rp in have:
            if rp != want and not rp.startswith("@"):
                cmd += ["-delete_rpath", rp]  # an absolute rpath leads back to the build machine
        if want not in have:
            cmd += ["-add_rpath", want]
        if len(cmd) > 1:
            subprocess.run(cmd + [f], check=True, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
