const form = document.querySelector("#query-form");
const station = document.querySelector("#station");
const status = document.querySelector("#status");
const results = document.querySelector("#observations");

function render(items) {
  results.replaceChildren(...items.map((item) => {
    const card = document.createElement("article");
    card.className = "observation";
    const heading = document.createElement("h2");
    heading.textContent = item.station;
    const temperature = document.createElement("p");
    temperature.className = "metric";
    const temperatureLabel = document.createElement("span");
    temperatureLabel.textContent = "Temperature";
    const temperatureValue = document.createElement("strong");
    temperatureValue.textContent = `${item.temperatureCelsius.toFixed(1)} °C`;
    temperature.append(temperatureLabel, temperatureValue);
    const salinity = document.createElement("p");
    salinity.className = "metric";
    const salinityLabel = document.createElement("span");
    salinityLabel.textContent = "Salinity";
    const salinityValue = document.createElement("strong");
    salinityValue.textContent = `${item.salinityPsu.toFixed(1)} PSU`;
    salinity.append(salinityLabel, salinityValue);
    card.append(heading, temperature, salinity);
    return card;
  }));
  status.textContent = `${items.length} observation${items.length === 1 ? "" : "s"}`;
}

async function query() {
  status.textContent = "Querying product…";
  const parameter = station.value ? `?station=${encodeURIComponent(station.value)}` : "";
  try {
    const response = await fetch(`api/observations${parameter}`);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const payload = await response.json();
    render(payload.items);
  } catch (error) {
    status.textContent = `The product could not answer: ${error.message}`;
    results.replaceChildren();
  }
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  query();
});

query();
