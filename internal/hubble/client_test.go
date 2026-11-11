package hubble

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilium/cilium/api/v1/flow"
	"github.com/cilium/cilium/api/v1/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/wrapperspb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

type fakeObserver struct {
	observer.UnimplementedObserverServer
	flows     []*flow.Flow
	endStream bool
}

func (s *fakeObserver) GetFlows(_ *observer.GetFlowsRequest, st grpc.ServerStreamingServer[observer.GetFlowsResponse]) error {
	for _, f := range s.flows {
		if err := st.Send(&observer.GetFlowsResponse{
			ResponseTypes: &observer.GetFlowsResponse_Flow{Flow: f},
		}); err != nil {
			return err
		}
	}
	if !s.endStream {
		<-st.Context().Done()
	}
	return nil
}

type sink struct {
	mu    sync.Mutex
	flows []Flow
	caps  []model.Capability
}

func (s *sink) Publish(events ...model.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range events {
		if f, ok := ev.Data.(Flow); ok {
			s.flows = append(s.flows, f)
		}
	}
	return nil
}

func (s *sink) onCap(c model.Capability) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps = append(s.caps, c)
}

func (s *sink) snapshot() ([]Flow, []model.Capability) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.flows), slices.Clone(s.caps)
}

func threeTierModel() *model.Model {
	m := model.New()
	var changes []model.Change
	for _, name := range []string{"web", "api", "cache"} {
		lbl := modeltest.Labels("app.kubernetes.io/name", name)
		d := modeltest.Deployment("vk-demo", name, 2, lbl)
		rs := modeltest.ReplicaSet(d, "h1")
		p1 := modeltest.Running(modeltest.Scheduled(modeltest.PodOf(rs, "1"), "node-1"), true)
		p2 := modeltest.Running(modeltest.Scheduled(modeltest.PodOf(rs, "2"), "node-1"), true)
		svc := modeltest.Service("vk-demo", name, lbl)
		for _, o := range []runtime.Object{d, rs, p1, p2, svc} {
			changes = append(changes, model.Change{Obj: o})
		}
	}
	barePod := modeltest.Running(modeltest.Scheduled(modeltest.Pod("vk-demo", "probe-curl", nil, nil), "node-1"), true)
	changes = append(changes, model.Change{Obj: barePod})
	m.Apply(changes...)
	return m
}

func relayServiceObj(ns string, port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: relayService},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: port}}},
	}
}

func startFakeGRPC(t *testing.T, flows []*flow.Flow, opts ...grpc.ServerOption) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(opts...)
	observer.RegisterObserverServer(srv, &fakeObserver{flows: flows})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

func mapDial(wantAddr, realAddr string) DialFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		if addr != wantAddr {
			return nil, fmt.Errorf("unexpected dial address %q, want %q", addr, wantAddr)
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", realAddr)
	}
}

func sampleFlows() []*flow.Flow {
	ep := func(pod string) *flow.Endpoint {
		return &flow.Endpoint{Namespace: "vk-demo", PodName: pod}
	}
	return []*flow.Flow{
		{Verdict: flow.Verdict_FORWARDED, Source: ep("probe-curl"), Destination: ep("web-h1-1"), TrafficDirection: flow.TrafficDirection_INGRESS},
		{Verdict: flow.Verdict_FORWARDED, Source: ep("web-h1-1"), Destination: ep("api-h1-1"), TrafficDirection: flow.TrafficDirection_INGRESS},
		{Verdict: flow.Verdict_FORWARDED, Source: ep("web-h1-2"), Destination: ep("api-h1-2"), TrafficDirection: flow.TrafficDirection_INGRESS},
		// Reply packet in reverse direction must be ignored so api->web is not invented.
		{Verdict: flow.Verdict_FORWARDED, Source: ep("api-h1-1"), Destination: ep("web-h1-1"), IsReply: wrapperspb.Bool(true)},
		// Intra-app pod-to-pod traffic must be ignored.
		{Verdict: flow.Verdict_FORWARDED, Source: ep("web-h1-1"), Destination: ep("web-h1-2")},
		// Sender egress trace before receiver ingress policy drop: only DROPPED should flush.
		{
			Verdict:            flow.Verdict_FORWARDED,
			Source:             ep("web-h1-1"),
			DestinationService: &flow.Service{Namespace: "vk-demo", Name: "cache"},
			TrafficDirection:   flow.TrafficDirection_EGRESS,
		},
		{
			Verdict:            flow.Verdict_DROPPED,
			Source:             ep("web-h1-1"),
			Destination:        ep("cache-h1-1"),
			DestinationService: &flow.Service{Namespace: "vk-demo", Name: "cache"},
			TrafficDirection:   flow.TrafficDirection_INGRESS,
		},
	}
}

func TestAutoDetectCiliumPlaintextAggregatesFlows(t *testing.T) {
	realAddr := startFakeGRPC(t, sampleFlows())
	cs := fake.NewClientset(relayServiceObj(ciliumNamespace, 80))
	out := &sink{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	r := New(Deps{
		Client:        cs,
		Model:         threeTierModel(),
		Out:           out,
		OnCapability:  out.onCap,
		Dial:          mapDial("hubble-relay.kube-system.svc:80", realAddr),
		FlushInterval: 25 * time.Millisecond,
		RetryInterval: 50 * time.Millisecond,
	})
	go r.Run(ctx)

	waitFor(t, func() bool {
		flows, caps := out.snapshot()
		return len(flows) >= 3 && len(caps) >= 1
	})
	cancel()

	flows, caps := out.snapshot()
	if caps[0].State != string(probe.OK) {
		t.Fatalf("capability = %+v, want ok", caps[0])
	}
	want := []Flow{
		{Src: "app/vk-demo/Deployment/web", Dst: "app/vk-demo/Deployment/api", Verdict: VerdictForwarded, Count: 2},
		{Src: "app/vk-demo/Deployment/web", Dst: "app/vk-demo/Deployment/cache", Verdict: VerdictDropped, Count: 1},
		{Src: "app/vk-demo/Pod/probe-curl", Dst: "app/vk-demo/Deployment/web", Verdict: VerdictForwarded, Count: 1},
	}
	if !slices.Equal(flows[:3], want) {
		t.Fatalf("flows = %+v, want %+v", flows, want)
	}
}

type slowSnapshotModel struct {
	ModelReader
	once sync.Once
}

func (m *slowSnapshotModel) Snapshot() ([]model.Entity, []model.Edge) {
	m.once.Do(func() { time.Sleep(15 * time.Millisecond) })
	return m.ModelReader.Snapshot()
}

func TestShortStreamFlushesAllBufferedFlowsBeforeEOF(t *testing.T) {
	burst := make([]*flow.Flow, 64)
	for i := range burst {
		burst[i] = &flow.Flow{
			Verdict:          flow.Verdict_FORWARDED,
			Source:           &flow.Endpoint{Namespace: "vk-demo", PodName: "web-h1-1"},
			Destination:      &flow.Endpoint{Namespace: "vk-demo", PodName: "api-h1-1"},
			TrafficDirection: flow.TrafficDirection_INGRESS,
		}
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	observer.RegisterObserverServer(srv, &fakeObserver{flows: burst, endStream: true})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	cs := fake.NewClientset(relayServiceObj(ciliumNamespace, 80))
	out := &sink{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	r := New(Deps{
		Client:        cs,
		Model:         &slowSnapshotModel{ModelReader: threeTierModel()},
		Out:           out,
		OnCapability:  out.onCap,
		Dial:          mapDial("hubble-relay.kube-system.svc:80", ln.Addr().String()),
		FlushInterval: 10 * time.Second,
		RetryInterval: 5 * time.Second,
	})
	go r.Run(ctx)

	waitFor(t, func() bool {
		flows, _ := out.snapshot()
		return len(flows) >= 1
	})
	cancel()

	flows, _ := out.snapshot()
	if flows[0].Count != int64(len(burst)) {
		t.Fatalf("flushed count = %d, want all %d buffered flows before EOF", flows[0].Count, len(burst))
	}
}

func TestAutoDetectGKEDPv2mTLS(t *testing.T) {
	pki := mintPKI(t, dpv2ServerName)
	srvTLS := &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientCAs:    pki.caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
	realAddr := startFakeGRPC(t, sampleFlows(), grpc.Creds(credentials.NewTLS(srvTLS)))
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: dpv2Namespace, Name: dpv2CertSecret},
		Data:       map[string][]byte{"ca.crt": pki.caPEM, "tls.crt": pki.clientCertPEM, "tls.key": pki.clientKeyPEM},
	}
	cs := fake.NewClientset(relayServiceObj(dpv2Namespace, 443), sec)
	out := &sink{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	r := New(Deps{
		Client:        cs,
		Model:         threeTierModel(),
		Out:           out,
		OnCapability:  out.onCap,
		Dial:          mapDial("hubble-relay.gke-managed-dpv2-observability.svc:443", realAddr),
		FlushInterval: 25 * time.Millisecond,
		RetryInterval: 50 * time.Millisecond,
	})
	go r.Run(ctx)

	waitFor(t, func() bool {
		flows, caps := out.snapshot()
		return len(flows) >= 2 && len(caps) >= 1
	})
	cancel()

	_, caps := out.snapshot()
	if caps[0].State != string(probe.OK) {
		t.Fatalf("dpv2 capability = %+v, want ok", caps[0])
	}
}

func TestGKEDPv2RefusesUnverifiedServerCert(t *testing.T) {
	clientPKI := mintPKI(t, dpv2ServerName)
	roguePKI := mintPKI(t, dpv2ServerName)
	wrongSANPKI := mintPKIWithCA(t, clientPKI, "wrong-relay.example.internal")

	for name, srvCert := range map[string]tls.Certificate{
		"unknown CA": roguePKI.serverCert,
		"wrong SAN":  wrongSANPKI,
	} {
		t.Run(name, func(t *testing.T) {
			srvTLS := &tls.Config{Certificates: []tls.Certificate{srvCert}, MinVersion: tls.VersionTLS12}
			realAddr := startFakeGRPC(t, sampleFlows(), grpc.Creds(credentials.NewTLS(srvTLS)))
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: dpv2Namespace, Name: dpv2CertSecret},
				Data: map[string][]byte{
					"ca.crt":  clientPKI.caPEM,
					"tls.crt": clientPKI.clientCertPEM,
					"tls.key": clientPKI.clientKeyPEM,
				},
			}
			cs := fake.NewClientset(relayServiceObj(dpv2Namespace, 443), sec)
			out := &sink{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			r := New(Deps{
				Client:        cs,
				Model:         threeTierModel(),
				Out:           out,
				OnCapability:  out.onCap,
				Dial:          mapDial("hubble-relay.gke-managed-dpv2-observability.svc:443", realAddr),
				FlushInterval: 25 * time.Millisecond,
				RetryInterval: 200 * time.Millisecond,
			})
			go r.Run(ctx)

			waitFor(t, func() bool {
				_, caps := out.snapshot()
				return len(caps) >= 1
			})
			cancel()

			flows, caps := out.snapshot()
			if len(flows) != 0 {
				t.Fatalf("accepted %d flows over unverified TLS", len(flows))
			}
			if caps[0].State != string(probe.Error) {
				t.Fatalf("capability = %+v, want state %q (never ok or unreachable)", caps[0], probe.Error)
			}
		})
	}
}

func TestAbsentRelayAndPrometheusFallback(t *testing.T) {
	t.Run("absent relay with no prometheus", func(t *testing.T) {
		cs := fake.NewClientset()
		out := &sink{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := New(Deps{Client: cs, Model: threeTierModel(), Out: out, OnCapability: out.onCap, RetryInterval: 100 * time.Millisecond})
		go r.Run(ctx)
		waitFor(t, func() bool {
			_, caps := out.snapshot()
			return len(caps) >= 1
		})
		cancel()
		flows, caps := out.snapshot()
		if len(flows) != 0 || caps[0].State != string(probe.Missing) {
			t.Fatalf("got flows=%v caps=%v, want 0 flows and missing", flows, caps)
		}
	})

	t.Run("absent relay falls back to prometheus pull", func(t *testing.T) {
		var gotQuery string
		prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query().Get("query")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[`+
				`{"metric":{"__name__":"traces_service_graph_request_total","client":"vk-demo/web","server":"vk-demo/api"},"value":[1,"3.4"]},`+
				`{"metric":{"__name__":"traces_service_graph_request_failed_total","client":"vk-demo/web","server":"vk-demo/api"},"value":[1,"2.0"]},`+
				`{"metric":{"__name__":"traces_service_graph_request_total","client":"web","server":"api"},"value":[1,"4.0"]},`+
				`{"metric":{"__name__":"traces_service_graph_request_total","client":"api","server":"cache"},"value":[1,"2.1"]},`+
				`{"metric":{"__name__":"hubble_flows_processed_total","source":"vk-demo/web","destination":"vk-demo/cache","verdict":"DROPPED"},"value":[1,"1.2"]}`+
				`]}}`)
		}))
		defer prom.Close()
		u, _ := url.Parse(prom.URL)
		hc := &http.Client{Transport: roundTripTo(u.Host)}
		promSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "prometheus-server"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 9090}}},
		}
		cs := fake.NewClientset(promSvc)
		m := threeTierModel()
		// Add a second Deployment named "web" in another namespace: bare client="web" is ambiguous and must be skipped.
		dupWeb := modeltest.Deployment("other-ns", "web", 1, modeltest.Labels("app.kubernetes.io/name", "web"))
		dupRS := modeltest.ReplicaSet(dupWeb, "h1")
		dupPod := modeltest.Running(modeltest.Scheduled(modeltest.PodOf(dupRS, "1"), "node-1"), true)
		m.Apply(model.Change{Obj: dupWeb}, model.Change{Obj: dupRS}, model.Change{Obj: dupPod})

		out := &sink{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		r := New(Deps{Client: cs, Model: m, Out: out, OnCapability: out.onCap, HTTPClient: hc, RetryInterval: 200 * time.Millisecond})
		go r.Run(ctx)
		waitFor(t, func() bool {
			flows, caps := out.snapshot()
			return len(flows) >= 3 && len(caps) >= 1
		})
		cancel()

		if strings.Contains(gotQuery, "traces_service_graph_request_failed_total") {
			t.Fatalf("promQuery double-counts request_failed_total: %q", gotQuery)
		}
		flows, caps := out.snapshot()
		if caps[0].State != string(probe.Missing) {
			t.Fatalf("hubble capability = %+v, want missing", caps[0])
		}
		want := []Flow{
			{Src: "app/vk-demo/Deployment/api", Dst: "app/vk-demo/Deployment/cache", Verdict: VerdictForwarded, Count: 2},
			{Src: "app/vk-demo/Deployment/web", Dst: "app/vk-demo/Deployment/api", Verdict: VerdictForwarded, Count: 3},
			{Src: "app/vk-demo/Deployment/web", Dst: "app/vk-demo/Deployment/cache", Verdict: VerdictDropped, Count: 1},
		}
		if !slices.Equal(flows[:3], want) {
			t.Fatalf("prom flows = %+v, want %+v", flows, want)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func roundTripTo(host string) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Host = host
		return http.DefaultTransport.RoundTrip(clone)
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

type testPKI struct {
	caKey         *ecdsa.PrivateKey
	caCert        *x509.Certificate
	caPEM         []byte
	caPool        *x509.CertPool
	serverCert    tls.Certificate
	clientCertPEM []byte
	clientKeyPEM  []byte
}

func mintPKI(t *testing.T, serverSAN string) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-hubble-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)

	pki := testPKI{caKey: caKey, caCert: caCert, caPEM: caPEM, caPool: pool}
	pki.serverCert = mintPKIWithCA(t, pki, serverSAN)
	clientCertPEM, clientKeyPEM := mintLeafPEM(t, caCert, caKey, "", x509.ExtKeyUsageClientAuth, 3)
	pki.clientCertPEM = clientCertPEM
	pki.clientKeyPEM = clientKeyPEM
	return pki
}

func mintPKIWithCA(t *testing.T, pki testPKI, serverSAN string) tls.Certificate {
	t.Helper()
	certPEM, keyPEM := mintLeafPEM(t, pki.caCert, pki.caKey, serverSAN, x509.ExtKeyUsageServerAuth, 2)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func mintLeafPEM(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, dns string, usage x509.ExtKeyUsage, serial int64) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	if dns != "" {
		tmpl.DNSNames = []string{dns}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}
