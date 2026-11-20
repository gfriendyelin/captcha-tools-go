import base64
import io
import json
from pathlib import Path
from PIL import Image

w, h = 17, 13
raw = bytes((37 * x + 61 * y + 29 * c) % 256 for y in range(h) for x in range(w) for c in range(3))
image = Image.frombytes("RGB", (w, h), raw)
buffer = io.BytesIO()
image.save(buffer, format="JPEG")
encoded = buffer.getvalue()
decoded = Image.open(io.BytesIO(encoded)).convert("RGB").tobytes()
payload = {
    "width": w,
    "height": h,
    "raw": base64.b64encode(raw).decode(),
    "jpeg": base64.b64encode(encoded).decode(),
    "decoded": base64.b64encode(decoded).decode(),
}
Path(__file__).with_name("jpeg_golden.json").write_text(json.dumps(payload, indent=2), encoding="utf-8")
