# Writes ../index.html from docs/index.html with asset paths pointing into docs/,
# so the site shows whether GitHub Pages publishes from the repo root or from /docs.
import pathlib, re
here = pathlib.Path(__file__).parent
s = (here / "index.html").read_text(encoding="utf-8")
def fix(m):
    attr, url = m.group(1), m.group(2)
    if re.match(r"^(https?:|mailto:|#|data:|/)", url):
        return m.group(0)
    return f'{attr}="docs/{url}"'
s = re.sub(r'\b(src|href)="([^"]+)"', fix, s)
s = s.replace("<!doctype html>", "<!doctype html>\n<!-- generated from docs/index.html by docs/make_root.py; edit that file instead -->", 1)
(here.parent / "index.html").write_text(s, encoding="utf-8")
print("wrote index.html")
