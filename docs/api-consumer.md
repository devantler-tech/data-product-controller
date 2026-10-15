# Consume the Harbour API from an application

The [Go example](../examples/api-consumer/main.go) reads the named Harbour
observations output directly. An application owns its transport, authorization
and presentation; the controller supplies descriptions only. This example uses
public, synthetic observations and sends no cookies or authorization headers.
It does not provide a general-purpose client library or execute arbitrary query
protocols.

## Run a disposable proof

From the repository root, use Go at the version in [go.mod](../go.mod) or newer:

```bash
go test ./examples/api-consumer -run TestConsumesNamedHarbourOutput -count=1 -v
go test ./examples/api-consumer -run TestCommandUsesRegularDescriptorAndPublishesValidatedJSON -count=1 -v
```

The tests export metadata through the real registry handler and run the Harbour
handler behind a temporary TLS server. The caller trusts only that test server's
certificate. Success shows `PASS` after reading the Nordhavn observation with
temperature `16.8` and salinity `14.2`. The second test runs the command entry
point with a regular descriptor file. Both tests clean up their server and
temporary files. No cluster, account, credentials or deployed feature changes
are required.

## Use a published Harbour output

You need an existing registry with discovery enabled and a Harbour output served
over HTTPS. Obtain the endpoint from the product owner or your application's
configuration independently of the descriptor. The local HTTP demo from the
project README is not accepted by this HTTPS-only example.

From the repository root, replace the two example hostnames and the product
namespace/name with your own published reference product. Export its descriptor
without following redirects:

```bash
curl --fail --max-time 10 --max-filesize 65536 --proto '=https' \
  --output descriptor.json \
  'https://registry.example/api/v2/products/products/harbour-observations'

go run ./examples/api-consumer \
  --descriptor descriptor.json \
  --output observations \
  --approved-endpoint 'https://harbour.example/api/observations' \
  --station nordhavn
```

Success prints one JSON envelope containing the validated observation. Omit
`--station` to request both stations. Errors go to standard error, return a
nonzero exit status. Invalid input and failed queries publish no observations;
an output-writer failure can leave a partial output stream. Press Ctrl+C to cancel a
running request. Remove the caller-created `descriptor.json` when finished.

The approved endpoint must match the selected output's URL exactly. A
descriptor's recorded readiness is an observation, not permission or a guarantee
that the current data request will succeed. The example does not fetch contract,
documentation, UI, input or lineage URLs.

## Supported example profile

The example reads only the public descriptor version, kind and named output
projection. It accepts `data-product-descriptor/v1` and `DataProduct`, with
unique output names. The selected output must declare `OpenAPI`,
`application/json`, a literal HTTPS data URL and a literal HTTPS contract URL.
URLs are at most 1,024 bytes and contain no percent escapes. The data URL must
have no existing query, including an empty `?`; only the caller's station filter
is appended. This is narrower than the general descriptor URL profile.
Other descriptor metadata is inert; this reader is not a complete descriptor
schema validator. Use the [discovery schema](registry-discovery.md#bounds-failures-and-schema-compatibility)
and its independent validators when your application displays or exports the
whole descriptor.

The Harbour response has an `items` array of records with exact
`station`, `temperatureCelsius`, `salinityPsu` and `observedAt` fields.
Measurements preserve fractional values. Records require a valid timestamp;
a requested station must match every returned record.

Local input is a regular file of at most 64 KiB. The query has a ten-second
deadline, reads at most 1 MiB even without a Content-Length header, and accepts
at most 64 records. JSON must be complete UTF-8 with unique object keys, at most
32 nested levels and a bounded token count. Invalid or partial reads publish no
results. Transport and publisher diagnostics are replaced with fixed errors.

The command uses normal TLS verification and disables environment proxies.
The consumer function accepts a caller-owned HTTP client for application use;
its transport remains the caller's responsibility. It copies the client before
rejecting redirects and disabling its cookie jar, leaving shared client state
unchanged. TLS bypasses, inferred credentials and a registry query proxy are
outside this example.
