export type Location = { latitude: number; longitude: number };
export function validLocation(value: unknown): value is Location {
  if (!value || typeof value !== 'object') return false;
  const { latitude, longitude } = value as Location;
  return Number.isFinite(latitude) && Math.abs(latitude) <= 90
    && Number.isFinite(longitude) && Math.abs(longitude) <= 180;
}

// NOAA fractional-year approximation, geometric horizon -0.833 degrees
// (solar radius + refraction). Evaluating elevation also handles polar day/night.
// https://gml.noaa.gov/grad/solcalc/solareqns.PDF
export function solarTheme(now: Date, location: Location): 'light' | 'dark' {
  const rad = Math.PI / 180;
  const day = (Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()) - Date.UTC(now.getUTCFullYear(), 0, 1)) / 86400000 + 1;
  const hour = now.getUTCHours() + now.getUTCMinutes() / 60 + now.getUTCSeconds() / 3600;
  const yearDays = (Date.UTC(now.getUTCFullYear() + 1, 0, 1) - Date.UTC(now.getUTCFullYear(), 0, 1)) / 86400000;
  const gamma = 2 * Math.PI / yearDays * (day - 1 + (hour - 12) / 24);
  const equation = 229.18 * (0.000075 + 0.001868 * Math.cos(gamma) - 0.032077 * Math.sin(gamma)
    - 0.014615 * Math.cos(2 * gamma) - 0.040849 * Math.sin(2 * gamma));
  const declination = 0.006918 - 0.399912 * Math.cos(gamma) + 0.070257 * Math.sin(gamma)
    - 0.006758 * Math.cos(2 * gamma) + 0.000907 * Math.sin(2 * gamma)
    - 0.002697 * Math.cos(3 * gamma) + 0.00148 * Math.sin(3 * gamma);
  const angle = (hour * 60 + equation + 4 * location.longitude) / 4 - 180;
  const elevationSin = Math.sin(location.latitude * rad) * Math.sin(declination)
    + Math.cos(location.latitude * rad) * Math.cos(declination) * Math.cos(angle * rad);
  return elevationSin >= Math.sin(-0.833 * rad) ? 'light' : 'dark';
}
