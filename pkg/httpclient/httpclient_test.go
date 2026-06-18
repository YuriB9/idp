package httpclient_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/pkg/httpclient"
)

func TestMapStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code    int
		wantErr error
	}{
		{code: http.StatusOK, wantErr: nil},
		{code: http.StatusNoContent, wantErr: nil},
		{code: http.StatusNotFound, wantErr: errs.ErrNotFound},
		{code: http.StatusConflict, wantErr: errs.ErrConflict},
		{code: http.StatusUnauthorized, wantErr: errs.ErrUnauthorized},
		{code: http.StatusForbidden, wantErr: errs.ErrForbidden},
		{code: http.StatusBadRequest, wantErr: errs.ErrValidation},
	}
	for _, tc := range tests {
		err := httpclient.MapStatus(tc.code)
		if tc.wantErr == nil && err != nil {
			t.Fatalf("status %d: want nil, got %v", tc.code, err)
		}
		if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
			t.Fatalf("status %d: want %v, got %v", tc.code, tc.wantErr, err)
		}
	}
}

func TestMapStatus_UnexpectedIsError(t *testing.T) {
	t.Parallel()
	if err := httpclient.MapStatus(http.StatusInternalServerError); err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestNew_ReturnsClient(t *testing.T) {
	t.Parallel()
	c := httpclient.New(httpclient.Config{})
	if c == nil || c.Transport == nil {
		t.Fatal("expected configured client with transport")
	}
}
