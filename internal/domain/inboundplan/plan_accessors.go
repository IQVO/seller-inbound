package inboundplan

// The read accessors of the plan aggregate, split from plan.go at the
// accessor seam (file-length gate).

// ID returns the planId.
func (p *Plan) ID() ID { return p.id }

// SellerID returns the owner of the planned stock.
func (p *Plan) SellerID() string { return p.sellerID }

// DestinationNodeID returns the node the stock is sent to.
func (p *Plan) DestinationNodeID() string { return p.destination }

// Packing returns the packing option.
func (p *Plan) Packing() PackingOption { return p.packing }

// Lines returns a copy of the planned lines.
func (p *Plan) Lines() []Line { return append([]Line(nil), p.lines...) }

// PlacementOption returns who splits the inbound (ADR 0015).
func (p *Plan) PlacementOption() PlacementOption { return p.placementOption }
