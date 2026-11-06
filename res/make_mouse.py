# Cuts the white background off res/mouse-source.jpg and writes res/mouse.webp (used by the UI).
from PIL import Image, ImageDraw
import numpy as np, pathlib, sys
from collections import deque
root = pathlib.Path(__file__).parent
im = Image.open(root / "mouse-source.jpg").convert("RGB")
a = np.asarray(im).astype(int)[2:-2, 2:-2]          # drop JPEG edge noise
H, W = a.shape[:2]
diff = (255 - a).sum(2)
tol = 60
bgmask = np.zeros((H, W), bool)
seen = np.zeros((H, W), bool)
q = deque([(0, x) for x in range(W)] + [(H - 1, x) for x in range(W)] + [(y, 0) for y in range(H)] + [(y, W - 1) for y in range(H)])
while q:
    y, x = q.popleft()
    if seen[y, x]:
        continue
    seen[y, x] = True
    if diff[y, x] > tol:
        continue
    bgmask[y, x] = True
    if x > 0: q.append((y, x - 1))
    if x < W - 1: q.append((y, x + 1))
    if y > 0: q.append((y - 1, x))
    if y < H - 1: q.append((y + 1, x))
alpha = np.full((H, W), 255, np.uint8)
alpha[bgmask] = 0
soft = bgmask & (diff > tol / 2)
alpha[soft] = ((diff[soft] - tol / 2) / (tol / 2) * 255).astype(np.uint8)
ys, xs = np.where(alpha > 128)
x0, x1, y0, y1 = xs.min(), xs.max(), ys.min(), ys.max()
out = Image.fromarray(np.dstack([a.astype(np.uint8), alpha]), "RGBA").crop((x0, y0, x1 + 1, y1 + 1))
k = 1000 / max(out.size)
small = out.resize((round(out.width * k), round(out.height * k)), Image.LANCZOS)
small.save(root / "mouse.webp", "WEBP", quality=88, method=6)
print("mouse.webp", small.size, "crop", (x0, y0, x1, y1))
if "--grid" in sys.argv:
    g = Image.new("RGB", small.size, (255, 255, 255)); g.paste(small, (0, 0), small); d = ImageDraw.Draw(g)
    for x in range(0, g.width, 25): d.line([(x, 0), (x, g.height)], fill=(255, 0, 0) if x % 100 == 0 else (255, 205, 205))
    for y in range(0, g.height, 25): d.line([(0, y), (g.width, y)], fill=(255, 0, 0) if y % 100 == 0 else (255, 205, 205))
    for x in range(0, g.width, 100): d.text((x + 2, 2), str(x), fill=(0, 0, 255))
    for y in range(0, g.height, 100): d.text((2, y + 2), str(y), fill=(0, 0, 255))
    g.crop((0, 0, 650, 750)).save("/tmp/claude-0/grid2.png")
