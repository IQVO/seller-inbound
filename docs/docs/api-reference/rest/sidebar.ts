import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/seller-inbound-api",
    },
    {
      type: "category",
      label: "Sellers",
      link: {
        type: "doc",
        id: "api-reference/rest/sellers",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/onboard-seller",
          label: "Onboard a seller (OnboardSeller)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/suspend-seller",
          label: "Suspend a seller (SuspendSeller)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/reinstate-seller",
          label: "Reinstate a seller (ReinstateSeller)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-seller-scorecard",
          label: "Get a seller's compliance scorecard (GetSellerScorecard)",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Inbound Plans",
      link: {
        type: "doc",
        id: "api-reference/rest/inbound-plans",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/create-inbound-plan",
          label: "Create an inbound plan (CreateInboundPlan)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-inbound-plan",
          label: "Get an inbound plan (GetInboundPlan)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/confirm-inbound-plan",
          label: "Confirm an inbound plan (ConfirmInboundPlan)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-inbound-plan",
          label: "Cancel an inbound plan (CancelInboundPlan)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Inbound Shipments",
      link: {
        type: "doc",
        id: "api-reference/rest/inbound-shipments",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/create-inbound-shipment",
          label: "Open an inbound shipment (CreateInboundShipment)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/add-carton",
          label: "Add a carton (AddCarton)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/label-inbound-shipment",
          label: "Label an inbound shipment (LabelShipment)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-shipment-labels",
          label: "Get the label data (GetLabels)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/ship-inbound-shipment",
          label: "Ship an inbound shipment (ShipInboundShipment)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-inbound-shipment",
          label: "Get an inbound shipment (GetInboundShipment)",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
