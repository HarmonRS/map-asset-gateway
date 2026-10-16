# Demo Scripts

This folder contains runnable demos for the internal remote-sensing result service.

## Prerequisites

- PostgreSQL and PostGIS are available to the API through `config/service.env`.
- The Python environment is available at `PYTHON_EXE`.
- Go is installed and can run `go run`.

## Service Startup

You can start the stack manually from the repository root:

```powershell
.\start-all.bat
```

Or use the self-contained full-flow demo to start a temporary stack on demo ports:

```powershell
.\demo\demo_full_flow.ps1 -StartStack -StartCallbackReceiver
```

## Demo List

- `demo_import_and_slice.ps1`
  Imports a raster result, binds a style, and triggers COG, preview, legend, and tile generation.
- `demo_register_target_system.ps1`
  Creates or updates a callback-based target system.
- `demo_push_result.ps1`
  Pushes an existing managed result to a target system or callback URL.
- `demo_geojson_flow.ps1`
  Registers a GeoJSON resource and optionally pushes it through the same delivery interface.
- `demo_full_flow.ps1`
  Runs the main end-to-end flow: sample raster, import, slice, target registration, and callback push.
- `demo_callback_receiver.py`
  Small local receiver for inspecting push payloads.

## Suggested Order

1. Start the services manually, or run `demo_full_flow.ps1 -StartStack -StartCallbackReceiver`.
2. Run `demo_import_and_slice.ps1` to create a managed result.
3. Run `demo_register_target_system.ps1` to register a callback receiver.
4. Run `demo_push_result.ps1` to publish the managed result.
5. Run `demo_geojson_flow.ps1 -Push` if you want to verify the retained GeoJSON delivery path.

## Example Commands

```powershell
.\demo\demo_import_and_slice.ps1
.\demo\demo_register_target_system.ps1 -CallbackUrl http://127.0.0.1:8920/callback
.\demo\demo_push_result.ps1 -ResultCode demo_result_20260406_120000 -TargetSystemCode demo_callback
.\demo\demo_geojson_flow.ps1 -Push -CallbackUrl http://127.0.0.1:8920/callback
.\demo\demo_full_flow.ps1 -StartStack -StartCallbackReceiver
```

## Output

Each demo prints a JSON summary that includes the main identifiers and URLs. Temporary run data is written under `temp/demo-runs/`.

`source_system_code` is intentionally left empty in the demos. If you want to populate it, seed the `source_systems` table first.
