// Copyright © 2019 The Things Network Foundation, The Things Industries B.V.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package networkserver

import (
	"testing"

	"github.com/mohae/deepcopy"
	"github.com/smartystreets/assertions"
	"go.thethings.network/lorawan-stack/pkg/events"
	"go.thethings.network/lorawan-stack/pkg/ttnpb"
	"go.thethings.network/lorawan-stack/pkg/util/test"
	"go.thethings.network/lorawan-stack/pkg/util/test/assertions/should"
)

func TestHandleRxParamSetupAns(t *testing.T) {
	for _, tc := range []struct {
		Name             string
		Device, Expected *ttnpb.EndDevice
		Payload          *ttnpb.MACCommand_RxParamSetupAns
		AssertEvents     func(*testing.T, ...events.Event) bool
		Error            error
	}{
		{
			Name: "nil payload",
			Device: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{},
			},
			Expected: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{},
			},
			AssertEvents: func(t *testing.T, evs ...events.Event) bool {
				return assertions.New(t).So(evs, should.BeEmpty)
			},
			Error: errNoPayload,
		},
		{
			Name: "no request",
			Device: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{},
			},
			Expected: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{},
			},
			Payload: &ttnpb.MACCommand_RxParamSetupAns{
				Rx1DataRateOffsetAck: true,
				Rx2DataRateIndexAck:  true,
				Rx2FrequencyAck:      true,
			},
			AssertEvents: func(t *testing.T, evs ...events.Event) bool {
				a := assertions.New(t)
				return a.So(evs, should.HaveLength, 1) &&
					a.So(evs[0].Name(), should.Equal, "ns.mac.rx_param_setup.answer.accept") &&
					a.So(evs[0].Data(), should.Resemble, &ttnpb.MACCommand_RxParamSetupAns{
						Rx1DataRateOffsetAck: true,
						Rx2DataRateIndexAck:  true,
						Rx2FrequencyAck:      true,
					})
			},
			Error: errMACRequestNotFound,
		},
		{
			Name: "all ack",
			Device: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{
					CurrentParameters: ttnpb.MACParameters{
						Rx1DataRateOffset: 99,
						Rx2Frequency:      99,
					},
					PendingRequests: []*ttnpb.MACCommand{
						(&ttnpb.MACCommand_RxParamSetupReq{
							Rx1DataRateOffset: 42,
							Rx2DataRateIndex:  43,
							Rx2Frequency:      44,
						}).MACCommand(),
					},
				},
			},
			Expected: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{
					CurrentParameters: ttnpb.MACParameters{
						Rx1DataRateOffset: 42,
						Rx2DataRateIndex:  43,
						Rx2Frequency:      44,
					},
					PendingRequests: []*ttnpb.MACCommand{},
				},
			},
			Payload: &ttnpb.MACCommand_RxParamSetupAns{
				Rx1DataRateOffsetAck: true,
				Rx2DataRateIndexAck:  true,
				Rx2FrequencyAck:      true,
			},
			AssertEvents: func(t *testing.T, evs ...events.Event) bool {
				a := assertions.New(t)
				return a.So(evs, should.HaveLength, 1) &&
					a.So(evs[0].Name(), should.Equal, "ns.mac.rx_param_setup.answer.accept") &&
					a.So(evs[0].Data(), should.Resemble, &ttnpb.MACCommand_RxParamSetupAns{
						Rx1DataRateOffsetAck: true,
						Rx2DataRateIndexAck:  true,
						Rx2FrequencyAck:      true,
					})
			},
		},
		{
			Name: "data rate ack",
			Device: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{
					CurrentParameters: ttnpb.MACParameters{
						Rx1DataRateOffset: 99,
						Rx2Frequency:      99,
					},
					PendingRequests: []*ttnpb.MACCommand{
						(&ttnpb.MACCommand_RxParamSetupReq{
							Rx1DataRateOffset: 42,
							Rx2DataRateIndex:  43,
							Rx2Frequency:      44,
						}).MACCommand(),
					},
				},
			},
			Expected: &ttnpb.EndDevice{
				MACState: &ttnpb.MACState{
					CurrentParameters: ttnpb.MACParameters{
						Rx1DataRateOffset: 99,
						Rx2Frequency:      99,
					},
					PendingRequests: []*ttnpb.MACCommand{},
				},
			},
			Payload: &ttnpb.MACCommand_RxParamSetupAns{
				Rx1DataRateOffsetAck: true,
				Rx2DataRateIndexAck:  true,
				Rx2FrequencyAck:      false,
			},
			AssertEvents: func(t *testing.T, evs ...events.Event) bool {
				a := assertions.New(t)
				return a.So(evs, should.HaveLength, 1) &&
					a.So(evs[0].Name(), should.Equal, "ns.mac.rx_param_setup.answer.reject") &&
					a.So(evs[0].Data(), should.Resemble, &ttnpb.MACCommand_RxParamSetupAns{
						Rx1DataRateOffsetAck: true,
						Rx2DataRateIndexAck:  true,
						Rx2FrequencyAck:      false,
					})
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			a := assertions.New(t)

			dev := deepcopy.Copy(tc.Device).(*ttnpb.EndDevice)

			var err error
			evs := collectEvents(func() {
				err = handleRxParamSetupAns(test.Context(), dev, tc.Payload)
			})
			if tc.Error != nil && !a.So(err, should.EqualErrorOrDefinition, tc.Error) ||
				tc.Error == nil && !a.So(err, should.BeNil) {
				t.FailNow()
			}
			a.So(dev, should.Resemble, tc.Expected)
			a.So(tc.AssertEvents(t, evs...), should.BeTrue)
		})
	}
}
