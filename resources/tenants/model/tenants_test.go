// Copyright 2017 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package model_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	. "github.com/mendersoftware/deployments/resources/tenants/model"
	mstore "github.com/mendersoftware/deployments/resources/tenants/store/mocks"
)

func TestProvisionTenant(t *testing.T) {
	testCases := []struct {
		id string

		storeErr error

		err error
	}{
		{
			id: "foo",
		},
		{
			id:       "foo",
			storeErr: errors.New("connection failed"),
			err:      errors.New("failed to provision tenant: connection failed"),
		},
	}

	for i := range testCases {
		tc := testCases[i]

		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			s := mstore.Store{}
			s.On("ProvisionTenant",
				mock.MatchedBy(
					func(_ context.Context) bool {
						return true
					}),
				tc.id).Return(tc.storeErr)

			m := NewModel(&s)

			ctx := context.Background()

			err := m.ProvisionTenant(ctx, tc.id)
			if tc.err != nil {
				assert.EqualError(t, err, tc.err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
