// health.go — thermal & power sensor readings for a Chassis.
//
// Redfish exposes these as inline arrays on two fat resources:
//   /Chassis/{id}/Thermal  → { Fans: [...], Temperatures: [...] }
//   /Chassis/{id}/Power    → { PowerSupplies: [...], PowerControl: [...] }
//
// That means each call is a single GET (no per-member fan-out) — cheap
// compared to the hardware inventory. But BMCs return reading values
// that need interpretation:
//   - Fan.Reading may be RPM or % depending on ReadingUnits.
//   - Temperature.ReadingCelsius is a float; some BMCs report int.
//   - PowerControl.PowerConsumedWatts aggregates all PSUs.
//
// Anything the BMC doesn't populate renders as "—" upstream — same
// contract as hardware.go.
//
// Deprecation note
// ────────────────
// Redfish 2023+ deprecated /Thermal and /Power in favor of /ThermalSubsystem
// and /PowerSubsystem with a flatter model. Nothing in our target
// fleet (iLO4/iLO5/iDRAC9) uses the new paths yet; when they do we add
// detection in firstChassisURL.

package redfish

import (
	"context"
	"fmt"
)

// Fan is one cooling fan — chassis or CPU.
type Fan struct {
	Name         string `json:"name"`
	Reading      int    `json:"reading"`       // raw value
	ReadingUnits string `json:"reading_units"` // "RPM" / "Percent"
	Health       string `json:"health"`
	State        string `json:"state"`
}

// TempSensor is one temperature probe — CPU/DIMM/inlet/outlet.
type TempSensor struct {
	Name                 string  `json:"name"`
	ReadingCelsius       float64 `json:"reading_celsius"`
	UpperCriticalCelsius float64 `json:"upper_critical_celsius,omitempty"`
	UpperWarningCelsius  float64 `json:"upper_warning_celsius,omitempty"`
	Health               string  `json:"health"`
	State                string  `json:"state"`
}

// PSU is one power supply unit.
type PSU struct {
	Name               string  `json:"name"`
	Model              string  `json:"model"`
	SerialNumber       string  `json:"serial_number"`
	Manufacturer       string  `json:"manufacturer"`
	PowerCapacityWatts int     `json:"power_capacity_watts"`
	LineInputVoltage   float64 `json:"line_input_voltage,omitempty"`
	InputType          string  `json:"input_type,omitempty"` // "AC" / "DC"
	Health             string  `json:"health"`
	State              string  `json:"state"`
}

// PowerConsumption is the aggregate draw across the whole chassis.
// ConsumedWatts is instantaneous; Avg/Min/Max are over a BMC-defined
// window (usually ~30s).
type PowerConsumption struct {
	ConsumedWatts        int `json:"consumed_watts"`
	AverageConsumedWatts int `json:"average_consumed_watts,omitempty"`
	MinConsumedWatts     int `json:"min_consumed_watts,omitempty"`
	MaxConsumedWatts     int `json:"max_consumed_watts,omitempty"`
	CapacityWatts        int `json:"capacity_watts,omitempty"`
}

// Health is the combined thermal + power snapshot returned to the API
// layer. Each field has a sibling *Err string — a failure in one block
// (e.g. Power endpoint 404s) leaves the other intact.
type Health struct {
	Fans         []Fan            `json:"fans"`
	Temperatures []TempSensor     `json:"temperatures"`
	PSUs         []PSU            `json:"psus"`
	Power        PowerConsumption `json:"power"`

	ThermalErr string `json:"thermal_error,omitempty"`
	PowerErr   string `json:"power_error,omitempty"`
}

// Thermal returns fan + temperature readings for the first chassis.
// Single GET. BMCs sometimes return 404 for /Thermal on severely
// under-featured boxes (desktop-grade iDRACs); caller should render as
// an empty section, not a hard error.
func (c *Client) Thermal(ctx context.Context) (fans []Fan, temps []TempSensor, err error) {
	chassisURL, err := c.firstChassisURL(ctx)
	if err != nil {
		return nil, nil, err
	}
	if chassisURL == "" {
		return nil, nil, nil
	}

	type rawStatus struct {
		Health string `json:"Health"`
		State  string `json:"State"`
	}
	type rawFan struct {
		Name         string    `json:"Name"`
		FanName      string    `json:"FanName"` // older iLO4 alias
		Reading      int       `json:"Reading"`
		ReadingUnits string    `json:"ReadingUnits"`
		Status       rawStatus `json:"Status"`
	}
	type rawTemp struct {
		Name                 string    `json:"Name"`
		ReadingCelsius       float64   `json:"ReadingCelsius"`
		UpperCritical        float64   `json:"UpperThresholdCritical"`
		UpperWarning         float64   `json:"UpperThresholdNonCritical"`
		Status               rawStatus `json:"Status"`
	}
	var doc struct {
		Fans         []rawFan  `json:"Fans"`
		Temperatures []rawTemp `json:"Temperatures"`
	}
	if err := c.getJSON(ctx, c.absURL(chassisURL+"/Thermal"), &doc); err != nil {
		return nil, nil, fmt.Errorf("thermal: %w", err)
	}

	fans = make([]Fan, 0, len(doc.Fans))
	for _, f := range doc.Fans {
		name := f.Name
		if name == "" {
			name = f.FanName
		}
		fans = append(fans, Fan{
			Name:         name,
			Reading:      f.Reading,
			ReadingUnits: f.ReadingUnits,
			Health:       f.Status.Health,
			State:        f.Status.State,
		})
	}
	temps = make([]TempSensor, 0, len(doc.Temperatures))
	for _, t := range doc.Temperatures {
		temps = append(temps, TempSensor{
			Name:                 t.Name,
			ReadingCelsius:       t.ReadingCelsius,
			UpperCriticalCelsius: t.UpperCritical,
			UpperWarningCelsius:  t.UpperWarning,
			Health:               t.Status.Health,
			State:                t.Status.State,
		})
	}
	return fans, temps, nil
}

// Power returns PSU inventory + aggregate consumption.
// Consumption is taken from the first PowerControl entry — nearly all
// servers expose exactly one, aggregating across all PSUs. If the BMC
// happens to publish multiple domains (rare outside of blade chassis)
// we'd sum them; for v1 we trust "the first one covers the system".
func (c *Client) Power(ctx context.Context) (psus []PSU, consumption PowerConsumption, err error) {
	chassisURL, err := c.firstChassisURL(ctx)
	if err != nil {
		return nil, PowerConsumption{}, err
	}
	if chassisURL == "" {
		return nil, PowerConsumption{}, nil
	}

	type rawStatus struct {
		Health string `json:"Health"`
		State  string `json:"State"`
	}
	type rawPSU struct {
		Name               string    `json:"Name"`
		Model              string    `json:"Model"`
		Manufacturer       string    `json:"Manufacturer"`
		SerialNumber       string    `json:"SerialNumber"`
		PowerCapacityWatts int       `json:"PowerCapacityWatts"`
		LineInputVoltage   float64   `json:"LineInputVoltage"`
		InputType          string    `json:"InputType"` // AC / DC
		Status             rawStatus `json:"Status"`
	}
	type rawPowerControl struct {
		PowerConsumedWatts int `json:"PowerConsumedWatts"`
		PowerCapacityWatts int `json:"PowerCapacityWatts"`
		PowerMetrics       struct {
			AverageConsumedWatts int `json:"AverageConsumedWatts"`
			MinConsumedWatts     int `json:"MinConsumedWatts"`
			MaxConsumedWatts     int `json:"MaxConsumedWatts"`
		} `json:"PowerMetrics"`
	}
	var doc struct {
		PowerSupplies []rawPSU          `json:"PowerSupplies"`
		PowerControl  []rawPowerControl `json:"PowerControl"`
	}
	if err := c.getJSON(ctx, c.absURL(chassisURL+"/Power"), &doc); err != nil {
		return nil, PowerConsumption{}, fmt.Errorf("power: %w", err)
	}

	psus = make([]PSU, 0, len(doc.PowerSupplies))
	for _, p := range doc.PowerSupplies {
		psus = append(psus, PSU{
			Name: p.Name, Model: p.Model,
			Manufacturer:       p.Manufacturer,
			SerialNumber:       p.SerialNumber,
			PowerCapacityWatts: p.PowerCapacityWatts,
			LineInputVoltage:   p.LineInputVoltage,
			InputType:          p.InputType,
			Health:             p.Status.Health,
			State:              p.Status.State,
		})
	}
	if len(doc.PowerControl) > 0 {
		pc := doc.PowerControl[0]
		consumption = PowerConsumption{
			ConsumedWatts:        pc.PowerConsumedWatts,
			AverageConsumedWatts: pc.PowerMetrics.AverageConsumedWatts,
			MinConsumedWatts:     pc.PowerMetrics.MinConsumedWatts,
			MaxConsumedWatts:     pc.PowerMetrics.MaxConsumedWatts,
			CapacityWatts:        pc.PowerCapacityWatts,
		}
	}
	return psus, consumption, nil
}
