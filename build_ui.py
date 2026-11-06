import base64, pathlib
root = pathlib.Path(__file__).parent
src = (root/"ui/src.html").read_text(encoding="utf-8")
for w, name in (("400","Regular"),("500","Medium"),("600","SemiBold")):
    b = (root/"res"/"fonts"/f"plex-{name}.woff2").read_bytes()
    src = src.replace("{{FONT_%s}}" % w, base64.b64encode(b).decode())
from PIL import Image
m = root/"res"/"mouse.webp"
src = src.replace("{{MOUSE_WEBP}}", base64.b64encode(m.read_bytes()).decode())
w, h = Image.open(m).size
src = src.replace("{{MOUSE_W}}", str(w)).replace("{{MOUSE_H}}", str(h))
(root/"ui/index.html").write_text(src, encoding="utf-8")
print("index.html", len(src)//1024, "KB")
