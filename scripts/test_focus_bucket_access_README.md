# Test access to OCI FOCUS cost reports

The script uses the standard Oracle-managed cost-report location:

- Namespace: `bling`
- Bucket: customer tenancy OCID
- Prefix: `FOCUS Reports/`

Set the same environment variables used by the loader:

```bash
export OCI_CLI_CONFIG_FILE=/home/opc/.oci/config
export OCI_CONFIG_PROFILE=DEFAULT
export OCI_TENANCY='ocid1.tenancy.oc1..example'
export REGION=eu-frankfurt-1
```

Then run:

```bash
chmod +x test_focus_bucket_access.sh
./test_focus_bucket_access.sh
```

Optional overrides:

```bash
FOCUS_NAMESPACE=bling \
FOCUS_BUCKET="$OCI_TENANCY" \
FOCUS_PREFIX='FOCUS Reports/' \
./test_focus_bucket_access.sh
```

The first request displays up to five objects. The second follows all pages and
prints the total matching object count.
