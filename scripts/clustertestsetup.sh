#/bin/bash
set -e
cd "$(dirname "$0")"

cd ./testyaml
# Create test namespace with different configurations
ls . | xargs -n 1 kubectl apply -f 
