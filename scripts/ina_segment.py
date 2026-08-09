#!/usr/bin/env python3
"""运行 inaSpeechSegmenter，并为 Hikami-Go 输出稳定的 JSON 格式。"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from inaSpeechSegmenter import Segmenter


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--ffmpeg", default="ffmpeg")
    parser.add_argument("--batch-size", type=int, default=256)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    segmenter = Segmenter(
        vad_engine="smn",
        detect_gender=False,
        ffmpeg=args.ffmpeg,
        batch_size=args.batch_size,
    )
    raw_segments = segmenter(args.input)
    segments = [
        {
            "label": str(label),
            "start_ms": round(float(start) * 1000),
            "end_ms": round(float(end) * 1000),
        }
        for label, start, end in raw_segments
    ]
    payload = {
        "engine": "ina-smn",
        "detect_gender": False,
        "segments": segments,
    }
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
