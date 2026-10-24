package outbox

import (
	"errors"
	"strings"
	"testing"
)

func TestSendFailureKeepsTheRunbookWordingAndUnwraps(t *testing.T) {
	cause := errors.New("broker down")
	f := &SendFailure{RowID: 7, Message: Message{EventType: "com.retailnetwork.partner.seller-inbound.seller.SellerOnboarded"}, Attempts: 2, Err: cause}
	if got := f.Error(); !strings.HasPrefix(got, "outbox row 7 (") || !strings.HasSuffix(got, "): broker down") {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(f, cause) {
		t.Fatal("Unwrap must expose the send error")
	}
}
