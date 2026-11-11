module github.com/DIYgod/pinky

go 1.27.3

require (
	github.com/Eyevinn/mp4ff v0.62.0
	github.com/Microsoft/go-winio v0.6.4
	github.com/anthropics/anthropic-sdk-go/v2 v2.9.0
	github.com/danielpaulus/go-ios v1.3.3-0.20261103131045-6910f3b585ad
	github.com/ebitengine/purego v0.11.3
	github.com/egoist/mygo v0.8.4-0.20261108101141-c8e911e32796
	github.com/godbus/dbus/v5 v5.2.4
	github.com/google/jsonschema-go v0.4.3
	github.com/google/uuid v1.6.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/pelletier/go-toml/v2 v2.4.3
	go.yaml.in/yaml/v3 v3.0.6
	golang.org/x/image v0.46.0
	golang.org/x/net v0.61.0
	golang.org/x/sys v0.49.0
	gvisor.dev/gvisor v0.0.0-20240405191320-0878b34101b5
	howett.net/plist v1.0.1
	modernc.org/sqlite v1.61.1
	rsc.io/qr v0.2.0
)

require (
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/grandcat/zeroconf v1.0.0 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/miekg/dns v1.1.57 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.mozilla.org/pkcs7 v0.9.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.33.0 // indirect
	go.opentelemetry.io/otel/metric v1.33.0 // indirect
	go.opentelemetry.io/otel/trace v1.33.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/exp v0.0.0-20230725093048-515e97ebf090 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240722135656-d784300faade // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	software.sslmate.com/src/go-pkcs12 v0.7.2 // indirect
)

tool github.com/egoist/mygo/cmd/mygo

// go-ios's main (6910f3b) plus one commit that pins the phone's TLS certificate to the
// pair record (branch pin-device-certificate of github.com/DIYgod/go-ios). Drop it once
// go-ios has the change; see "go-ios from main" in docs/notes/ios.md.
replace github.com/danielpaulus/go-ios => github.com/DIYgod/go-ios v0.0.0-20261108182015-53a1fc8154fc
