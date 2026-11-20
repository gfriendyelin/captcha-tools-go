#!/usr/bin/env python3
"""Export recognition baselines without modifying the original source tree."""
import argparse
import contextlib
import json
import os
from pathlib import Path
import random
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--images", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--group", required=True, choices=("pdl", "dfine", "multi"))
    args = parser.parse_args()
    source = args.source.resolve()
    images = args.images.resolve()
    output = args.output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    for name in ("use_pdl", "use_dfine", "use_multi"):
        os.environ[name] = "1" if name == "use_" + args.group else "0"
    records = []
    with tempfile.TemporaryDirectory(prefix="python-baseline-", dir=output.parent) as temporary, contextlib.chdir(temporary):
        sys.path.insert(0, str(source))
        import predict
        import crop_image
        for image_path in sorted(images.glob("*.jpg")):
            data = image_path.read_bytes()
            record = {"file": image_path.name, "bytes": len(data)}
            try:
                if args.group == "pdl":
                    crop_image.crop_image_v3(data)
                    cells = predict.predict_onnx_pdl(crop_image.validate_path)
                    record["cells"] = cells
                    record["points"] = [f"{col}_{row}" for row, col in cells]
                    with open(Path(crop_image.validate_path) / "cropped_9.jpg", "rb") as file:
                        icon = file.read()
                    with open(Path(crop_image.validate_path) / "nine.jpg", "rb") as file:
                        nine = file.read()
                    similar = predict.predict_onnx(icon, nine, None)
                    record["similarity_cells"] = similar
                    record["similarity_points"] = [f"{col}_{row}" for row, col in similar]
                elif args.group == "dfine":
                    points = predict.predict_onnx_dfine(data, False)
                    record["centers"] = [[float(x), float(y)] for x, y in points]
                    record["points"] = [f"{round(x / 333 * 10000)}_{round(y / 333 * 10000)}" for x, y in points]
                else:
                    crops = predict.predict_onnx_yolo(data)
                    record["yolo"] = {key: [item["bbox"] for item in value] for key, value in crops.items()}
                    original_randint = predict.random.randint
                    try:
                        predict.random.randint = lambda _low, _high: 0
                        points = predict.predict_dino_classify_pipeline(data, False)
                    finally:
                        predict.random.randint = original_randint
                    record["centers"] = [[float(x), float(y)] for x, y in points]
                    record["points"] = [f"{round(x / 333 * 10000)}_{round(y / 333 * 10000)}" for x, y in points]
            except Exception as error:
                record["error"] = f"{type(error).__name__}: {error}"
            records.append(record)
            print(json.dumps({"group": args.group, "file": image_path.name, "error": record.get("error")}, ensure_ascii=False), flush=True)
    payload = {"group": args.group, "python": sys.version, "models": args.group, "records": records}
    output.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")


if __name__ == "__main__":
    main()
