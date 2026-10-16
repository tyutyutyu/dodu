# Performance observations (2026-09-15)

Measured on Linux/amd64, Intel Core i7-11700K, Go 1.23.12.

- Stripped static Linux binary: 8.3 MiB.
- Real cold scan: 3.94 seconds, Docker 29.7.2, overlay2 on ext4,
  513 images, 32 containers, 179 volumes, 1950 build-cache records.
- Synthetic scanner benchmark: 428,518 ns/op, 320,853 B/op, 3,187 allocs/op
  for 2,000 images, 500 containers, 1,000 volumes; three measured iterations.

The mock benchmark returns in-memory slices. It does not measure daemon JSON
transfer, filesystem enumeration, or real disk performance. It cannot establish
cold-scan latency on arbitrary hosts. The real host continues running workloads,
so object counts and timings vary. A separate real run measured 3.8417 seconds cold and 0.0074 seconds warm,
with maximum child RSS across both runs of 25,180 KiB (Python time.monotonic and
resource.getrusage). These are observations on this host, not portable latency guarantees.

```sh
go test -tags perf -bench=BenchmarkScanLarge -benchmem -benchtime=3x ./test/perf/...
/usr/bin/time -f 'seconds=%e max_rss_kib=%M' bin/dodu --no-cache scan
/usr/bin/time -f 'seconds=%e max_rss_kib=%M' bin/dodu scan
/usr/bin/time -f 'seconds=%e max_rss_kib=%M' bin/dodu scan
```
