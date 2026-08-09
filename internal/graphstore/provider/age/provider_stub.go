//go:build !age

package age

import (
	"github.com/alibaba/UnifiedModel/internal/graphstore"
	"github.com/alibaba/UnifiedModel/pkg/contract"
	apperrors "github.com/alibaba/UnifiedModel/pkg/errors"
)

func init() {
	graphstore.RegisterProvider(graphstore.ProviderTypePostgresAge, func(config graphstore.ProviderConfig) (contract.GraphStore, error) {
		return nil, apperrors.New(apperrors.CodeProviderUnavailable, "postgres.age provider requires build tag 'age'")
	})
}
