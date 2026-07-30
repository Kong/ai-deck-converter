# e2e-tests

## Prerequisites
- Docker running locally.
- A Go toolchain + `make` (for `make build`).
- `curl`.

## Usage

1. Set the Kong license in `e2e-tests/license.json`
   2. This can be retrieved from the "Monthly Kong Gateway Enterprise License" secret in 1password


2. Run a test case:
   ```sh
   bash single_model_body_selector/test_script.sh
   bash single_model_header_selector/test_script.sh
   bash single_model_path_selector/test_script.sh
   bash multiple_model_shared_route_with_all_selector_types/test_script.sh
   ```

## Notes

- Path selector test cases are known to be incorrect and expected to fail.
