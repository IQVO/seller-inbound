# seller-inbound

Retail network, bounded context **Seller Inbound** (Supporting, partner tier).

PROPOSED. The seller-facing front door for stock coming into the network: a seller is onboarded, plans an inbound shipment to a vendor node in eaches (loose or case-packed), packs it into cartons each identified by an SSCC and a printed label, and ships it. The context checks the plan against the destination node's declared inbound limits and the catalogue item's packaging hierarchy, hands the vendor the shipment as a carton manifest through the Vendor API, and closes the shipment from the vendor's carton-level receipt, recording every discrepancy and compliance violation instead of correcting it. It absorbs the idea reserved earlier as the inventory-placement context; automated placement advice is not modelled (see the gap register).

Status: under construction. The design is proposed in
[retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound); `network/model/model.yaml` in IQVO/warehouse-docs is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.partner.seller-inbound.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
