//go:build !age

package age

import (
	"testing"

	"github.com/alibaba/UnifiedModel/internal/graphstore"
	apperrors "github.com/alibaba/UnifiedModel/pkg/errors"
)

func TestStubProviderIsRegisteredButUnavailable(t *testing.T) {
	providers := graphstore.RegisteredProviders()
	found := false
	for _, p := range providers {
		if p == graphstore.ProviderTypePostgresAge {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("postgres.age provider should be registered even in stub mode")
	}

	// Creating a provider should return an error
	_, err := graphstore.NewProvider(graphstore.ProviderConfig{
		Type: graphstore.ProviderTypePostgresAge,
	})
	if err == nil {
		t.Fatal("expected error when creating age provider without build tag")
	}
	appErr, ok := apperrors.As(err)
	if !ok {
		t.Fatalf("expected app error, got %T", err)
	}
	if appErr.Code != apperrors.CodeProviderUnavailable {
		t.Fatalf("expected CodeProviderUnavailable, got %v", appErr.Code)
	}
}
