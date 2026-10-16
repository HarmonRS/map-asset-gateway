import argparse
import os
import sys
from pathlib import Path


def bootstrap_gdal_env() -> None:
    env_root = Path(sys.executable).resolve().parent
    gdal_paths = [
        env_root / "Lib" / "site-packages" / "rasterio" / "gdal_data",
        env_root / "Library" / "share" / "gdal",
        env_root / "share" / "gdal",
    ]
    proj_paths = [
        env_root / "Lib" / "site-packages" / "rasterio" / "proj_data",
        env_root / "Library" / "share" / "proj",
        env_root / "share" / "proj",
    ]

    for path in gdal_paths:
        if path.exists():
            os.environ["GDAL_DATA"] = str(path)
            break
    for path in proj_paths:
        if path.exists():
            os.environ["PROJ_LIB"] = str(path)
            os.environ["PROJ_DATA"] = str(path)
            break


bootstrap_gdal_env()

import numpy as np
import rasterio
from rasterio.transform import from_origin


def build_values(width: int, height: int, preset: str) -> np.ndarray:
    yy, xx = np.mgrid[0:height, 0:width]
    x = (xx / max(width - 1, 1)).astype("float32")
    y = (yy / max(height - 1, 1)).astype("float32")

    if preset == "phase":
        values = np.sin((x * 8.0) + (y * 4.0)) * np.cos(y * 10.0)
    else:
        values = (x * 2.0 - 1.0) * 0.7 + np.sin(y * np.pi * 3.0) * 0.3

    return values.astype("float32")


def main() -> int:
    parser = argparse.ArgumentParser(description="Create a sample GeoTIFF for demos.")
    parser.add_argument("--output", required=True, help="Output GeoTIFF path.")
    parser.add_argument("--width", type=int, default=512)
    parser.add_argument("--height", type=int, default=512)
    parser.add_argument("--preset", choices=["ndvi", "phase"], default="ndvi")
    parser.add_argument("--origin-x", type=float, default=110.0)
    parser.add_argument("--origin-y", type=float, default=22.0)
    parser.add_argument("--pixel-size", type=float, default=0.01)
    args = parser.parse_args()

    output = Path(args.output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)

    data = build_values(args.width, args.height, args.preset)

    with rasterio.open(
        output,
        "w",
        driver="GTiff",
        width=args.width,
        height=args.height,
        count=1,
        dtype="float32",
        crs="EPSG:4326",
        transform=from_origin(args.origin_x, args.origin_y, args.pixel_size, args.pixel_size),
        nodata=-9999.0,
    ) as dst:
        dst.write(data, 1)

    print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
