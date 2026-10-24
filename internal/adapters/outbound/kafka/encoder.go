package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/seller-inbound/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/seller-inbound/internal/application/outbox"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// Topics this service publishes to (apis/asyncapi.yaml). The events other
// contexts consume leave through TopicEvents; the facts nobody consumes
// (the model marks them analytics only) leave through TopicAnalytics (ADR 0004).
const (
	TopicEvents    = "retailnetwork.seller-inbound.events"
	TopicAnalytics = "retailnetwork.seller-inbound.analytics"
)

// schemaVersion is the dataschema version of every payload below.
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON as in the AsyncAPI).
// Optional fields are omitted when unset; timestamps are RFC 3339 UTC;
// quantities are always eaches.

type sellerOnboardedData struct {
	SellerID    string `json:"sellerId"`
	Name        string `json:"name"`
	OnboardedAt string `json:"onboardedAt"`
}

type sellerSuspendedData struct {
	SellerID    string `json:"sellerId"`
	SuspendedAt string `json:"suspendedAt"`
	Reason      string `json:"reason,omitempty"`
}

type sellerReinstatedData struct {
	SellerID     string `json:"sellerId"`
	ReinstatedAt string `json:"reinstatedAt"`
}

type planLineData struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

type planConfirmedData struct {
	PlanID            string         `json:"planId"`
	SellerID          string         `json:"sellerId"`
	DestinationNodeID string         `json:"destinationNodeId"`
	PackingOption     string         `json:"packingOption"`
	Lines             []planLineData `json:"lines"`
	ConfirmedAt       string         `json:"confirmedAt"`
}

type contentData struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
	Lot           string `json:"lot,omitempty"`
	Expiry        string `json:"expiry,omitempty"`
}

type dimsData struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

type cartonData struct {
	SSCC         string        `json:"sscc"`
	ParentSSCC   string        `json:"parentSscc,omitempty"`
	Kind         string        `json:"kind"`
	Contents     []contentData `json:"contents"`
	GrossWeightG int           `json:"grossWeightG"`
	DimsMm       dimsData      `json:"dimsMm"`
}

type labelledData struct {
	ShipmentID string       `json:"shipmentId"`
	PlanID     string       `json:"planId"`
	SellerID   string       `json:"sellerId"`
	NodeID     string       `json:"nodeId"`
	Cartons    []cartonData `json:"cartons"`
	LabelledAt string       `json:"labelledAt"`
}

type shippedData struct {
	ShipmentID string `json:"shipmentId"`
	PlanID     string `json:"planId"`
	SellerID   string `json:"sellerId"`
	NodeID     string `json:"nodeId"`
	ShippedAt  string `json:"shippedAt"`
}

type receivedCartonData struct {
	SSCC   string `json:"sscc"`
	Status string `json:"status"`
}

type receivedData struct {
	ShipmentID string               `json:"shipmentId"`
	SellerID   string               `json:"sellerId"`
	NodeID     string               `json:"nodeId"`
	Status     string               `json:"status"`
	Cartons    []receivedCartonData `json:"cartons"`
	ReceivedAt string               `json:"receivedAt"`
}

type discrepancyData struct {
	ShipmentID string `json:"shipmentId"`
	SellerID   string `json:"sellerId"`
	NodeID     string `json:"nodeId"`
	SSCC       string `json:"sscc,omitempty"`
	Kind       string `json:"kind"`
	Expected   string `json:"expected,omitempty"`
	Actual     string `json:"actual,omitempty"`
	RecordedAt string `json:"recordedAt"`
}

type violationData struct {
	SellerID   string `json:"sellerId"`
	ShipmentID string `json:"shipmentId,omitempty"`
	Code       string `json:"code"`
	RecordedAt string `json:"recordedAt"`
}

// Encoder implements outbox.Encoder: each domain event becomes one
// outbox.Message holding the CloudEvents bytes, the Kafka key (the aggregate
// root id) and the content-type header. The CloudEvents `id` is minted HERE,
// once, and persisted with the outbox row, so a relay retry republishes the
// same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode encodes events in order.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		stream, topic := streamFor(ev)
		msg, err := buildMessage(ev, newID(), topic, stream, data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// streamFor routes the three events another context consumes to the events
// topic and every other fact to the analytics topic.
func streamFor(ev shared.Event) (stream, topic string) {
	switch ev.(type) {
	case inboundshipment.InboundShipmentLabelled, inboundshipment.InboundShipmentShipped, inboundshipment.InboundShipmentReceived:
		return cloudevents.StreamEvents, TopicEvents
	default:
		return cloudevents.StreamAnalytics, TopicAnalytics
	}
}

func buildMessage(ev shared.Event, id, topic, stream string, data any) (outbox.Message, error) {
	subject := ev.SubjectID()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: subject,
		Time: ev.OccurredAt(), Stream: stream, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID:    id,
		Topic:      topic,
		EventType:  cloudevents.Type(ev.Entity(), ev.EventName()),
		Subject:    subject,
		Key:        []byte(subject),
		DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion),
		Value:      value,
		Headers:    []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case seller.SellerOnboarded:
		return sellerOnboardedData{SellerID: string(e.SellerID), Name: e.Name, OnboardedAt: ts(e.At)}, nil
	case seller.SellerSuspended:
		return sellerSuspendedData{SellerID: string(e.SellerID), SuspendedAt: ts(e.At), Reason: e.Reason}, nil
	case seller.SellerReinstated:
		return sellerReinstatedData{SellerID: string(e.SellerID), ReinstatedAt: ts(e.At)}, nil
	case inboundplan.InboundPlanConfirmed:
		return planPayload(e), nil
	default:
		return shipmentPayload(ev)
	}
}

func planPayload(e inboundplan.InboundPlanConfirmed) planConfirmedData {
	lines := make([]planLineData, len(e.Lines))
	for i, l := range e.Lines {
		lines[i] = planLineData{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches}
	}
	return planConfirmedData{PlanID: string(e.PlanID), SellerID: e.SellerID, DestinationNodeID: e.DestinationNodeID,
		PackingOption: string(e.Packing), Lines: lines, ConfirmedAt: ts(e.At)}
}

func shipmentPayload(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case inboundshipment.InboundShipmentLabelled:
		return labelledPayload(e), nil
	case inboundshipment.InboundShipmentShipped:
		return shippedData{ShipmentID: string(e.ShipmentID), PlanID: e.PlanID, SellerID: e.SellerID, NodeID: e.NodeID, ShippedAt: ts(e.At)}, nil
	case inboundshipment.InboundShipmentReceived:
		cartons := make([]receivedCartonData, len(e.Cartons))
		for i, c := range e.Cartons {
			cartons[i] = receivedCartonData{SSCC: c.SSCC, Status: string(c.Status)}
		}
		return receivedData{ShipmentID: string(e.ShipmentID), SellerID: e.SellerID, NodeID: e.NodeID, Status: string(e.Status),
			Cartons: cartons, ReceivedAt: ts(e.ReceivedAt)}, nil
	case inboundshipment.InboundDiscrepancyRecorded:
		return discrepancyData{ShipmentID: string(e.ShipmentID), SellerID: e.SellerID, NodeID: e.NodeID, SSCC: e.SSCC, Kind: e.Kind,
			Expected: e.Expected, Actual: e.Actual, RecordedAt: ts(e.At)}, nil
	case inboundshipment.SellerComplianceViolationRecorded:
		return violationData{SellerID: e.SellerID, ShipmentID: string(e.ShipmentID), Code: e.Code, RecordedAt: ts(e.At)}, nil
	default:
		return nil, fmt.Errorf("kafka encoder: %s is not a published event", ev.EventName())
	}
}

func labelledPayload(e inboundshipment.InboundShipmentLabelled) labelledData {
	cartons := make([]cartonData, len(e.Cartons))
	for i, c := range e.Cartons {
		contents := make([]contentData, len(c.Contents))
		for j, ct := range c.Contents {
			contents[j] = contentData{CatalogItemID: ct.CatalogItemID, GTIN: ct.GTIN, QtyEaches: ct.QtyEaches, Lot: ct.Lot, Expiry: ct.Expiry}
		}
		cartons[i] = cartonData{SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: string(c.Kind), Contents: contents,
			GrossWeightG: c.GrossWeightG, DimsMm: dimsData{L: c.Dims.L, W: c.Dims.W, H: c.Dims.H}}
	}
	return labelledData{ShipmentID: string(e.ShipmentID), PlanID: e.PlanID, SellerID: e.SellerID, NodeID: e.NodeID,
		Cartons: cartons, LabelledAt: ts(e.At)}
}
