package contracts

import "time"

// Fixed fixtures: deterministic ids and times so golden files never churn.

var sampleTime = At(time.Date(2026, time.October, 21, 3, 15, 0, 0, time.UTC))

func at(minutes int) Time { return At(sampleTime.Add(time.Duration(minutes) * time.Minute)) }

func sampleWaypoint(name, address string, lat, lng float64, contact, phone string) Waypoint {
	return Waypoint{
		PlaceRef:     PlaceRef{Name: name, Address: address, Position: LatLng{Lat: lat, Lng: lng}, PlaceID: "pl-" + name},
		ContactName:  contact,
		ContactPhone: phone,
	}
}

func sampleStops() []RouteStop {
	pickup := sampleWaypoint("Kemang Village", "Jl. Pangeran Antasari No.36, Jakarta Selatan", -6.2694, 106.8143, "Dina", "081234567801")
	dropoff := sampleWaypoint("Plaza Senayan", "Jl. Asia Afrika No.8, Jakarta Pusat", -6.2253, 106.7995, "Raka", "081234567802")
	dropoff.Note = "Lobby barat"
	items := []StopItem{{
		Name: "Kardus sedang", CatalogKey: "box_m", Qty: 2, Fragile: true,
		Detail: Msg{Key: "dom.stopItem.detail", Params: map[string]any{"weightKg": 4, "dims": "40×30×30"}},
	}}
	return []RouteStop{
		{
			ID: "stop-1", Seq: 1, Kind: StopKindPickup, ShipmentID: "shp-1", Waypoint: pickup,
			LegKm: 2.4, LegMin: 8.5, EtaMin: 8.5, ArriveAt: at(9), Status: StopStatusWaiting,
			ItemsSummary: Msg{Key: "dom.itemsSummary", Params: map[string]any{"count": 2, "weightKg": 4}}, Items: items,
		},
		{
			ID: "stop-2", Seq: 2, Kind: StopKindDropoff, ShipmentID: "shp-1", Waypoint: dropoff,
			LegKm: 6.1, LegMin: 18, EtaMin: 29.5, ArriveAt: at(30), Status: StopStatusWaiting,
			ItemsSummary: Msg{Key: "dom.itemsSummary", Params: map[string]any{"count": 2, "weightKg": 4}}, Items: items, DeliveryCode: "4821",
		},
	}
}

func sampleRoute() Route {
	return Route{
		ID: "rt-1", PlanID: "plan-1", VehicleType: VehicleTypeMotor, Status: RouteStatusAwaitingDriver,
		StartPosition: LatLng{Lat: -6.2711, Lng: 106.8121}, StartAt: at(0), Stops: sampleStops(),
		DistanceKm: 8.5, DurationMin: 29.5, CostRp: 21500, TotalWeightKg: 4,
		Fare: []FareLine{
			{Label: Msg{Key: "dom.fare.base"}, AmountRp: 6000},
			{Label: Msg{Key: "dom.fare.distance", Params: map[string]any{"km": 6.1}}, AmountRp: 10980},
			{Label: Msg{Key: "dom.fare.time", Params: map[string]any{"min": 18}}, AmountRp: 2700},
			{Label: Msg{Key: "dom.fare.rounding"}, AmountRp: 1820},
		},
	}
}

func samplePlan() Plan {
	pickup := sampleStops()[0].Waypoint
	dropoff := sampleStops()[1].Waypoint
	route := sampleRoute()
	return Plan{
		ID: "plan-1", Code: "PL-2041", UserID: "cus-1", Status: PlanStatusAwaitingDriver,
		Schedule: Schedule{Mode: ScheduleModeNow},
		Priority: Priority{Preset: PresetBalanced, Text: "paling hemat tapi sampai sebelum jam 5"},
		Shipments: []Shipment{{
			ID: "shp-1", Pickup: pickup, Dropoff: dropoff, Deadline: at(120),
			Items: []Item{{
				ID: "itm-1", Name: "Kardus sedang", Qty: 2, WeightKg: 2, LengthCm: 40, WidthCm: 30, HeightCm: 30,
				Fragile: true, Source: ItemSourcePhoto, Confidence: new(0.92), CatalogKey: "box_m",
			}},
		}},
		SelectedOptionID: "plan-1:seimbang", Routes: []Route{route}, PaymentMethod: PaymentWallet, TotalCostRp: 21500,
		CreatedAt: at(-30), UpdatedAt: at(0), ConfirmedAt: at(0),
		Events:    []PlanEvent{{ID: "ev-1", At: at(0), Text: Msg{Key: "srv.api.event.confirmed", Params: map[string]any{"cost": 21500}}}},
		DraftStep: 4,
	}
}

func sampleDeliveryProof() *DeliveryProof {
	return &DeliveryProof{
		Kind: ProofKindPhoto, At: at(31), Photo: "data:image/jpeg;base64,/9j/4AAQSkZJRg==",
		Analysis: &ProofAnalysis{
			Summary: Msg{Key: "srv.proof.summary.ok"},
			Checks: []ProofCheck{
				{Key: ProofCheckRecipient, Result: ProofResultOK, Note: Msg{Key: "srv.proof.recipient.ok"}},
				{Key: ProofCheckParcel, Result: ProofResultUnclear, Note: Msg{Key: "srv.proof.parcel.unclear"}},
			},
			Confidence: 0.81, Model: "muse-spark", AnalyzedAt: at(31),
		},
	}
}

func sampleTopUp(status TopUpStatus) TopUp {
	topUp := TopUp{
		ID: "tup-1", UserID: "cus-1", AmountRp: 100000, Method: TopUpMethodVABCA, Status: status,
		CreatedAt: at(-60), ExpiresAt: at(1380), Reference: "8077000012345678",
	}
	if status == TopUpStatusSucceeded {
		topUp.PaidAt = at(-55)
	}
	return topUp
}

// samplePayloads has one payload per event type; a test keeps it in step with eventTopics.
func samplePayloads() map[string]any {
	route := sampleRoute()
	driverRoute := route
	driverRoute.VehicleID, driverRoute.DriverID, driverRoute.Status = "veh-1", "drv-1", RouteStatusAssigned
	driverRoute.AssignedAt = at(2)

	return map[string]any{
		TypePlanConfirmed: PlanConfirmed{
			Plan:    samplePlan(),
			Payment: Payment{Method: PaymentWallet, AmountRp: 21500, PaidAt: at(0)},
		},
		TypePlanCancelAsked:     PlanCancelAsked{PlanID: "plan-1", Reason: "salah alamat", AskedBy: "Rani Wulandari", Locale: "id"},
		TypePlanCancelRequested: PlanCancelRequested{PlanID: "plan-1", RequestID: "req-1", RequestedBy: CancelByCustomer, RouteIDs: []string{"rt-1"}, Reason: "salah alamat"},
		TypePlanCancelAccepted:  PlanCancelAccepted{PlanID: "plan-1", RequestID: "req-1", RouteID: "rt-1"},
		TypePlanCancelRejected:  PlanCancelRejected{PlanID: "plan-1", RequestID: "req-1", RouteID: "rt-1", Reason: Msg{Key: "srv.cancel.routeStarted"}},
		TypePlanCancelAborted:   PlanCancelAborted{PlanID: "plan-1", RequestID: "req-1", Reason: Msg{Key: "srv.cancel.routeStarted"}},
		TypePlanCancelled:       PlanCancelled{PlanID: "plan-1", RequestID: "req-1", RequestedBy: CancelByCustomer, Reason: "salah alamat", RefundRp: 21500},
		TypePlanRescheduled: PlanRescheduled{
			PlanID: "plan-1", Routes: []Route{route},
			Schedule: Schedule{Mode: ScheduleModeScheduled, Window: &TimeWindow{From: at(120), To: at(180)}},
		},
		TypeRouteAssigned: RouteAssigned{
			PlanID: "plan-1", PlanCode: "PL-2041", CustomerName: "Dina Pramesti",
			DriverID: "drv-1", VehicleID: "veh-1", StartAt: at(2), Route: driverRoute,
		},
		TypeRouteUnassigned: RouteUnassigned{PlanID: "plan-1", RouteID: "rt-1", DriverID: "drv-1", Reason: UnassignDriverOffline},
		TypeRouteStarted:    RouteStarted{PlanID: "plan-1", RouteID: "rt-1", DriverID: "drv-1", StartedAt: at(3)},
		TypeStopArrived:     StopArrived{PlanID: "plan-1", RouteID: "rt-1", StopID: "stop-1", ArrivedAt: at(11)},
		TypeStopCompleted:   StopCompleted{PlanID: "plan-1", RouteID: "rt-1", StopID: "stop-2", CompletedAt: at(31), Proof: sampleDeliveryProof()},
		TypeStopIssue: StopIssue{
			PlanID: "plan-1", RouteID: "rt-1", StopID: "stop-2", Kind: IssueKindNobodyThere, Note: "gerbang terkunci",
			Issue:      Msg{Key: "dom.issue.report", Params: map[string]any{"kind": "tidak_ada_orang", "note": "gerbang terkunci"}},
			ReportedAt: at(32),
		},
		TypeRouteCompleted: RouteCompleted{PlanID: "plan-1", RouteID: "rt-1", DriverID: "drv-1", FinishedAt: at(33)},
		TypeRouteDelayed:   RouteDelayed{PlanID: "plan-1", RouteID: "rt-1", DelayMin: 12.5},
		TypeDriverProvisioned: DriverProvisioned{
			DriverID: "drv-1", Name: "Budi Santoso", Phone: "081234567801", VehicleID: "veh-1",
			PinHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNo", JoinedAt: at(-1440),
		},
		TypeDriverUpdated:  DriverUpdated{DriverID: "drv-1", Name: "Budi Santoso", Phone: "081234567801", VehicleID: "veh-2"},
		TypeDriverOnline:   DriverOnline{DriverID: "drv-1", Position: &LatLng{Lat: -6.2711, Lng: 106.8121}},
		TypeDriverOffline:  DriverOffline{DriverID: "drv-1"},
		TypeDriverPosition: DriverPosition{DriverID: "drv-1", RouteID: "rt-1", Position: LatLng{Lat: -6.2688, Lng: 106.8132}, SpeedKmh: 27.4, RecordedAt: at(5)},
		TypeVehicleUpserted: VehicleUpserted{Vehicle: Vehicle{
			ID: "veh-1", Type: VehicleTypeMotor, Plate: "B 1234 ABC", Model: "Honda Vario 160",
			MaxWeightKg: 20, MaxVolumeM3: 0.12, MaxDimensionCm: 60, Status: VehicleStatusActive, DriverID: "drv-1",
			Fare: FareConfig{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10},
		}},
		TypeVehicleRemoved: VehicleRemoved{VehicleID: "veh-1"},
		TypeSettingsUpdated: SettingsUpdated{
			Key: SettingsKeyAI, AI: &AIFeatures{Vision: true, Voice: true, Intent: false, Copilot: true},
		},
		TypeCustomerRegistered: CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti", Email: "dina@contoh.id", Phone: "081234567800", CreatedAt: at(-2880)},
		TypeCustomerUpdated:    CustomerUpdated{CustomerID: "cus-1", Name: "Dina P.", Email: "dina@contoh.id", Phone: "081234567800"},
		TypeCustomerBlocked:    CustomerBlocked{CustomerID: "cus-1"},
		TypeCustomerUnblocked:  CustomerUnblocked{CustomerID: "cus-1"},
		TypeTopUpCreated:       TopUpCreated{TopUp: sampleTopUp(TopUpStatusPending)},
		TypeTopUpPaid:          TopUpPaid{TopUp: sampleTopUp(TopUpStatusSucceeded), TxID: "tx-1", BalanceAfterRp: 200000},
		TypeTopUpCancelled:     TopUpCancelled{TopUp: sampleTopUp(TopUpStatusCancelled)},
		TypeTopUpExpired:       TopUpExpired{TopUp: sampleTopUp(TopUpStatusExpired)},
		TypeWalletCharged:      WalletCharged{TxID: "tx-1", CustomerID: "cus-1", PlanID: "plan-1", AmountRp: 21500, BalanceAfterRp: 78500},
		TypeWalletRefunded:     WalletRefunded{TxID: "tx-2", CustomerID: "cus-1", PlanID: "plan-1", AmountRp: 21500, BalanceAfterRp: 100000},
	}
}

// sampleEnvelope wraps a sample payload with fixed, human-readable envelope fields.
func sampleEnvelope(eventType, source, key string, aggregateVersion int64, id string) Envelope {
	env, err := NewEnvelope(source, eventType, key, aggregateVersion, samplePayloads()[eventType])
	if err != nil {
		panic(err)
	}
	env.ID = id
	env.OccurredAt = sampleTime
	env.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	return env
}
