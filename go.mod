module github.com/grafana/mimir

go 1.23.7

// Please note that this directive is ignored when building with the Mimir build image,
// that will always use its bundled toolchain.
toolchain go1.24.2

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.6.0
	github.com/alecthomas/units v0.0.0-20240927000941-0f3dac36c52b
	github.com/dustin/go-humanize v1.0.1
	github.com/edsrzf/mmap-go v1.2.0
	github.com/failsafe-go/failsafe-go v0.6.9
	github.com/felixge/fgprof v0.9.5
	github.com/go-kit/log v0.2.1
	github.com/go-openapi/strfmt v0.23.0
	github.com/go-openapi/swag v0.23.1
	github.com/gogo/protobuf v1.3.2
	github.com/gogo/status v1.1.1
	github.com/golang/protobuf v1.5.4
	github.com/golang/snappy v1.0.0
	github.com/google/gopacket v1.1.19
	github.com/gorilla/mux v1.8.1
	github.com/grafana/dskit v0.0.0-20250422145853-90fa6b9a2b76
	github.com/grafana/e2e v0.1.2-0.20250306030804-b80b212be908
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/influxdata/influxdb/v2 v2.7.11
	github.com/json-iterator/go v1.1.12
	github.com/minio/minio-go/v7 v7.0.90
	github.com/mitchellh/go-wordwrap v1.0.1
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/opentracing-contrib/go-grpc v0.1.2
	github.com/opentracing-contrib/go-stdlib v1.1.0
	github.com/opentracing/opentracing-go v1.2.1-0.20220228012449-10b1cf09e00b
	github.com/pkg/errors v0.9.1
	github.com/prometheus/alertmanager v0.28.1
	github.com/prometheus/client_golang v1.21.1
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.63.0
	github.com/prometheus/prometheus v1.99.0
	github.com/segmentio/fasthash v1.0.3
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/afero v1.11.0
	github.com/stretchr/testify v1.10.0
	github.com/uber/jaeger-client-go v2.30.0+incompatible
	go.uber.org/atomic v1.11.0
	go.uber.org/goleak v1.3.0
	golang.org/x/crypto v0.36.0
	golang.org/x/net v0.38.0
	golang.org/x/sync v0.12.0
	golang.org/x/time v0.11.0
	google.golang.org/grpc v1.71.1
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	cloud.google.com/go/storage v1.43.0
	github.com/alecthomas/chroma/v2 v2.16.0
	github.com/alecthomas/kingpin/v2 v2.4.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.79.2
	github.com/dennwc/varint v1.0.0
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8
	github.com/google/go-cmp v0.7.0
	github.com/google/go-github/v57 v57.0.0
	github.com/google/uuid v1.6.0
	github.com/grafana-tools/sdk v0.0.0-20220919052116-6562121319fc
	github.com/grafana/alerting v0.0.0-20250411135245-cad0d384d430
	github.com/grafana/regexp v0.0.0-20240607082908-2cb410fa05da
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/hashicorp/vault/api v1.16.0
	github.com/influxdata/tdigest v0.0.2-0.20210216194612-fc98d27c9e8b
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822
	github.com/oklog/ulid/v2 v2.1.0
	github.com/okzk/sdnotify v0.0.0-20240725214427-1c1fdd37c5ac
	github.com/pierrec/lz4/v4 v4.1.22
	github.com/prometheus/otlptranslator v0.0.0-20250417063547-0a6a352a36dc
	github.com/prometheus/procfs v0.15.1
	github.com/shirou/gopsutil/v4 v4.24.12
	github.com/thanos-io/objstore v0.0.0-20250129163715-ec72e5a88a79
	github.com/tjhop/slog-gokit v0.1.4
	github.com/twmb/franz-go v1.18.1
	github.com/twmb/franz-go/pkg/kadm v1.14.0
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20250416232150-07cc0584a381
	github.com/twmb/franz-go/pkg/kmsg v1.9.0
	github.com/twmb/franz-go/plugin/kotel v1.5.0
	github.com/twmb/franz-go/plugin/kprom v1.1.0
	github.com/xlab/treeprint v1.2.0
	go.opentelemetry.io/collector/pdata v1.29.0
	go.opentelemetry.io/otel v1.35.0
	go.opentelemetry.io/otel/trace v1.35.0
	go.opentelemetry.io/proto/otlp v1.5.0
	go.uber.org/multierr v1.11.0
	golang.org/x/term v0.30.0
	google.golang.org/api v0.227.0
	google.golang.org/protobuf v1.36.6
	sigs.k8s.io/kustomize/kyaml v0.18.1
)

require (
	cloud.google.com/go/auth v0.15.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.7 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.17.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.8.2 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.10.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.3.3 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.1.1 // indirect
	github.com/Masterminds/sprig/v3 v3.2.1 // indirect
	github.com/at-wat/mqtt-go v0.19.4 // indirect
	github.com/aws/aws-sdk-go v1.55.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.29.1 // indirect
	github.com/bboreham/go-loser v0.0.0-20230920113527-fcc2c21820a3 // indirect
	github.com/ebitengine/purego v0.8.1 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.32.4 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-jose/go-jose/v4 v4.0.5 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-test/deep v1.1.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.2.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/golang/glog v1.2.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/grafana/pyroscope-go/godeltaprof v0.1.8 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.26.3 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.1.7 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-5 // indirect
	github.com/huandu/xstrings v1.3.2 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/knadh/koanf/maps v0.1.1 // indirect
	github.com/knadh/koanf/providers/confmap v0.1.0 // indirect
	github.com/knadh/koanf/v2 v2.1.2 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mdlayher/socket v0.4.1 // indirect
	github.com/mdlayher/vsock v1.2.1 // indirect
	github.com/minio/crc64nvme v1.0.1 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/exp/metrics v0.122.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatautil v0.122.0 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/processor/deltatocumulativeprocessor v0.122.0 // indirect
	github.com/pires/go-proxyproto v0.7.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/prometheus/sigv4 v0.1.2 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/sercand/kuberesolver/v6 v6.0.0 // indirect
	github.com/shopspring/decimal v1.2.0 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/collector/component v1.28.1 // indirect
	go.opentelemetry.io/collector/confmap v1.28.0 // indirect
	go.opentelemetry.io/collector/confmap/xconfmap v0.122.0 // indirect
	go.opentelemetry.io/collector/consumer v1.28.1 // indirect
	go.opentelemetry.io/collector/featuregate v1.28.0 // indirect
	go.opentelemetry.io/collector/pipeline v0.122.1 // indirect
	go.opentelemetry.io/collector/processor v0.122.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.59.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace v0.60.0 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/mail.v2 v2.3.1 // indirect
	gopkg.in/telebot.v3 v3.2.1 // indirect
	k8s.io/apimachinery v0.32.3 // indirect
	k8s.io/client-go v0.32.3 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	cloud.google.com/go/iam v1.2.2 // indirect
	github.com/DmitriyVTitov/size v1.5.0 // indirect
	github.com/HdrHistogram/hdrhistogram-go v1.1.2 // indirect
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/aws/aws-sdk-go-v2 v1.36.3 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.29.9 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.62 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.25.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.33.17 // indirect
	github.com/aws/smithy-go v1.22.3 // indirect
	github.com/benbjohnson/clock v1.3.5 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.14.3 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgraph-io/ristretto v0.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/docker/go-connections v0.4.1-0.20210727194412-58542c764a11 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/efficientgo/core v1.0.0-rc.3
	github.com/efficientgo/e2e v0.13.1-0.20220923082810-8fa9daa8af8a // indirect
	github.com/facette/natsort v0.0.0-20181210072756-2cd4dd1e2dcb // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.8.0 // indirect
	github.com/go-errors/errors v1.4.2 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/analysis v0.23.0 // indirect
	github.com/go-openapi/errors v0.22.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/loads v0.22.0 // indirect
	github.com/go-openapi/runtime v0.27.1 // indirect
	github.com/go-openapi/spec v0.21.0 // indirect
	github.com/go-openapi/validate v0.24.0 // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/gofrs/uuid v4.4.0+incompatible // indirect
	github.com/gogo/googleapis v1.4.1 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/pprof v0.0.0-20241210010833-40e02aabc2ad // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.6 // indirect
	github.com/googleapis/gax-go/v2 v2.14.1 // indirect
	github.com/gosimple/slug v1.1.1 // indirect
	github.com/grafana/gomemcache v0.0.0-20250318131618-74242eea118d
	github.com/hashicorp/consul/api v1.31.2 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-msgpack v1.1.5 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.6 // indirect
	github.com/hashicorp/memberlist v0.5.0 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/hashicorp/vault/api/auth/approle v0.9.0
	github.com/hashicorp/vault/api/auth/kubernetes v0.9.0
	github.com/hashicorp/vault/api/auth/userpass v0.8.0
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jessevdk/go-flags v1.5.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/julienschmidt/httprouter v1.3.0 // indirect
	github.com/klauspost/compress v1.18.0
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4
	github.com/miekg/dns v1.1.64 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/ncw/swift v1.0.53 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/common/sigv4 v0.1.0 // indirect
	github.com/prometheus/exporter-toolkit v0.14.0 // indirect
	github.com/rainycape/unidecode v0.0.0-20150907023854-cb7f23ec59be // indirect
	github.com/rs/cors v1.11.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/sean-/seed v0.0.0-20170313163322-e2103e2c3529 // indirect
	github.com/shurcooL/httpfs v0.0.0-20230704072500-f1e31cf0ba5c // indirect
	github.com/shurcooL/vfsgen v0.0.0-20200824052919-0d455de96546 // indirect
	github.com/spf13/cobra v1.8.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/uber/jaeger-lib v2.4.1+incompatible // indirect
	github.com/xhit/go-str2duration/v2 v2.1.0 // indirect
	go.etcd.io/etcd/api/v3 v3.5.4 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.5.4 // indirect
	go.etcd.io/etcd/client/v3 v3.5.4 // indirect
	go.mongodb.org/mongo-driver v1.14.0 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/collector/semconv v0.122.1
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.60.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/exp v0.0.0-20250106191152-7588d65b2ba8 // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/oauth2 v0.28.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/tools v0.31.0 // indirect
	google.golang.org/genproto v0.0.0-20241113202542-65e8d215514f // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250303144028-a0af3efb3deb // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250409194420-de1ac958c67a
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	k8s.io/utils v0.0.0-20241104100929-3ea5e8cea738 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

replace github.com/prometheus/prometheus => github.com/grafana/mimir-prometheus v1.8.2-0.20250414042702-d35df15de7be

// Replace memberlist with our fork which includes some fixes that haven't been
// merged upstream yet:
// - https://github.com/hashicorp/memberlist/pull/260
// - https://github.com/grafana/memberlist/pull/3
// - https://github.com/hashicorp/memberlist/pull/263
replace github.com/hashicorp/memberlist => github.com/grafana/memberlist v0.3.1-0.20220714140823-09ffed8adbbe

// gopkg.in/yaml.v3
// + https://github.com/go-yaml/yaml/pull/691
// + https://github.com/go-yaml/yaml/pull/876
replace gopkg.in/yaml.v3 => github.com/colega/go-yaml-yaml v0.0.0-20220720105220-255a8d16d094

// We are using our modified version of the upstream GO regexp (branch remotes/origin/speedup)
replace github.com/grafana/regexp => github.com/grafana/regexp v0.0.0-20240531075221-3685f1377d7b

// Replace goautoneg with a fork until https://github.com/munnerz/goautoneg/pull/6 is merged
replace github.com/munnerz/goautoneg => github.com/grafana/goautoneg v0.0.0-20240607115440-f335c04c58ce

// Replace opentracing-contrib/go-stdlib with a fork until https://github.com/opentracing-contrib/go-stdlib/pull/68 is merged.
replace github.com/opentracing-contrib/go-stdlib => github.com/grafana/opentracing-contrib-go-stdlib v0.0.0-20230509071955-f410e79da956

// Replace opentracing-contrib/go-grpc with a fork until https://github.com/opentracing-contrib/go-grpc/pull/16 is merged.
replace github.com/opentracing-contrib/go-grpc => github.com/charleskorn/go-grpc v0.0.0-20231024023642-e9298576254f

// Replacing prometheus/alertmanager with our fork.
replace github.com/prometheus/alertmanager => github.com/grafana/prometheus-alertmanager v0.25.1-0.20250417181314-6d0f5436a1fb

// Replacing with a fork commit based on v1.17.1 having cherry-picked the following PRs:
// - https://github.com/grafana/franz-go/pull/1
// - https://github.com/grafana/franz-go/pull/3
// - https://github.com/grafana/franz-go/pull/4
replace github.com/twmb/franz-go => github.com/grafana/franz-go v0.0.0-20241009100846-782ba1442937

// Pin Google GRPC to v1.65.0 as v1.66.0 has API changes and also potentially performance regressions.
// Following https://github.com/grafana/dskit/pull/581
replace google.golang.org/grpc => google.golang.org/grpc v1.65.0
