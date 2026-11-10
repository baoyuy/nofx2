package payment

import (
	"net/http"
	"testing"
	"time"

	"nofx/internal/apischeduler"
)

func TestScheduledPaidClientStillUsesSingleUseTransport(t *testing.T) {
	original := http.DefaultTransport.(*http.Transport).Clone()
	client := apischeduler.Wrap(&http.Client{Transport: original, Timeout: 3 * time.Second})
	paid := singleUsePaidClient(client)
	transport, ok := paid.Transport.(*http.Transport)
	if !ok || transport == original || !transport.DisableKeepAlives || transport.ForceAttemptHTTP2 {
		t.Fatalf("single-use protections lost behind scheduler: %T", paid.Transport)
	}
	if original.DisableKeepAlives {
		t.Fatal("mutated discovery transport")
	}
	if paid.Timeout != client.Timeout || paid.CheckRedirect == nil {
		t.Fatal("client settings lost")
	}
	if err := paid.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("paid redirect allowed")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification weakened")
	}
	if apischeduler.BaseTransport(apischeduler.Wrap(paid).Transport) != transport {
		t.Fatal("fresh paid transport replaced")
	}
}
