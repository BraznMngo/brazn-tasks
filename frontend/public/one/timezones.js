/**
 * BRA-1636 item 2: the time zone list a person picks from.
 *
 * The fork's `GET /api/v2/user/timezones` returns every name in the time zone database, legacy
 * aliases included (EST5EDT, Egypt, Eire, Etc/GMT+10), with no offset and no city. Sebastian asked
 * for a standard list instead, where every entry shows its UTC offset and a city.
 *
 * What is saved does not change: the zone id, such as `Europe/Berlin`, because the server works
 * out reminders and due dates from it. A fixed offset would be wrong for half the year anywhere
 * the clocks change, and a label is not something the server can read.
 *
 * No `Etc/GMT±N` name is ever offered: their sign is reversed, so `Etc/GMT+10` is UTC−10.
 */

/**
 * One zone for every offset people live in, and one for each large city a person looks for,
 * including the half-hour and 45-minute offsets (India +05:30, Nepal +05:45, Chatham +12:45).
 * Each is a canonical IANA id the server accepts. The city a person reads is the id's last part.
 */
export const STANDARD_ZONES = Object.freeze([
  'Pacific/Pago_Pago', 'Pacific/Honolulu', 'Pacific/Marquesas', 'America/Anchorage',
  'America/Los_Angeles', 'America/Vancouver', 'America/Tijuana', 'America/Denver',
  'America/Phoenix', 'America/Edmonton', 'America/Chicago', 'America/Mexico_City',
  'America/Winnipeg', 'America/Guatemala', 'America/Costa_Rica', 'America/New_York',
  'America/Toronto', 'America/Bogota', 'America/Lima', 'America/Havana', 'America/Halifax',
  'America/Caracas', 'America/Santiago', 'America/La_Paz', 'America/Santo_Domingo',
  'America/St_Johns', 'America/Sao_Paulo', 'America/Argentina/Buenos_Aires',
  'America/Montevideo', 'America/Noronha', 'Atlantic/South_Georgia', 'Atlantic/Azores',
  'Atlantic/Cape_Verde', 'UTC', 'Europe/London', 'Europe/Dublin', 'Europe/Lisbon',
  'Atlantic/Reykjavik', 'Africa/Abidjan', 'Africa/Casablanca', 'Europe/Berlin', 'Europe/Paris',
  'Europe/Madrid', 'Europe/Rome', 'Europe/Amsterdam', 'Europe/Brussels', 'Europe/Vienna',
  'Europe/Zurich', 'Europe/Stockholm', 'Europe/Oslo', 'Europe/Copenhagen', 'Europe/Warsaw',
  'Europe/Prague', 'Europe/Budapest', 'Africa/Lagos', 'Europe/Athens', 'Europe/Helsinki',
  'Europe/Kyiv', 'Europe/Bucharest', 'Africa/Cairo', 'Africa/Johannesburg', 'Asia/Jerusalem',
  'Asia/Beirut', 'Europe/Istanbul', 'Europe/Moscow', 'Asia/Riyadh', 'Asia/Baghdad',
  'Asia/Qatar', 'Africa/Nairobi', 'Asia/Tehran', 'Asia/Dubai', 'Asia/Baku', 'Asia/Tbilisi',
  'Europe/Samara', 'Asia/Kabul', 'Asia/Karachi', 'Asia/Tashkent', 'Asia/Yekaterinburg',
  'Asia/Kolkata', 'Asia/Colombo', 'Asia/Kathmandu', 'Asia/Dhaka', 'Asia/Almaty',
  'Asia/Yangon', 'Asia/Bangkok', 'Asia/Jakarta', 'Asia/Ho_Chi_Minh', 'Asia/Novosibirsk',
  'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Singapore', 'Asia/Taipei', 'Asia/Manila',
  'Asia/Kuala_Lumpur', 'Australia/Perth', 'Australia/Eucla', 'Asia/Tokyo', 'Asia/Seoul',
  'Asia/Yakutsk', 'Australia/Darwin', 'Australia/Adelaide', 'Australia/Brisbane',
  'Australia/Sydney', 'Australia/Melbourne', 'Pacific/Guam', 'Asia/Vladivostok',
  'Australia/Lord_Howe', 'Pacific/Noumea', 'Asia/Magadan', 'Pacific/Auckland', 'Pacific/Fiji',
  'Pacific/Chatham', 'Pacific/Tongatapu', 'Pacific/Apia', 'Pacific/Kiritimati',
]);

/**
 * `zone`'s offset from UTC at `now` in minutes, daylight saving time included, or null when this
 * browser does not know the zone.
 */
export function zoneOffsetMinutes(zone, now) {
  let name;
  try {
    name = new Intl.DateTimeFormat('en-US', {timeZone: zone, timeZoneName: 'longOffset'})
      .formatToParts(now)
      .find((part) => part.type === 'timeZoneName')?.value;
  } catch {
    return null;
  }
  // "GMT" alone is UTC itself; otherwise "GMT+05:30" or "GMT-03:00".
  const match = /^GMT(?:([+\-−])(\d{1,2})(?::(\d{2}))?)?$/.exec(name ?? '');
  if (match === null) return null;
  if (match[1] === undefined) return 0;
  const minutes = Number(match[2]) * 60 + Number(match[3] ?? 0);
  return match[1] === '+' ? minutes : -minutes;
}

/** "+05:30", "-03:00", "+00:00". */
function offsetText(minutes) {
  const sign = minutes < 0 ? '-' : '+';
  const whole = Math.abs(minutes);
  const hours = String(Math.floor(whole / 60)).padStart(2, '0');
  return `${sign}${hours}:${String(whole % 60).padStart(2, '0')}`;
}

/**
 * The city a person reads for `zone`: the id's last part with spaces, "Buenos Aires" for
 * `America/Argentina/Buenos_Aires`. An `Etc/` zone names no city, and its name would read with the
 * wrong sign, so it has none.
 */
function cityOf(zone) {
  if (zone.startsWith('Etc/')) return '';
  return zone.split('/').pop().replace(/_/g, ' ');
}

/** "(UTC+01:00) Berlin", read at `now`. A zone the browser does not know shows its id alone. */
export function zoneLabel(zone, now) {
  const minutes = zoneOffsetMinutes(zone, now);
  if (minutes === null) return zone;
  const city = cityOf(zone);
  return city === '' ? `(UTC${offsetText(minutes)})` : `(UTC${offsetText(minutes)}) ${city}`;
}

/**
 * The options the select shows: the standard zones the server lists, sorted by today's offset and
 * then by city, each labelled with `zoneLabel`. `serverZones` is the fork's own list; a standard
 * zone it does not list is left out, so nothing is offered that the server would refuse to save.
 *
 * `current` is the account's saved zone. When it is not among the options (an alias such as `GMT`)
 * it is still offered, labelled the same way, so the select shows this account's own zone rather
 * than whichever entry comes first.
 */
export function timezoneOptions(serverZones, current, now) {
  const listed = new Set(serverZones);
  const zones = STANDARD_ZONES.filter((zone) => listed.has(zone));
  if (current !== '' && !zones.includes(current)) zones.push(current);
  return zones
    .map((zone) => ({id: zone, offset: zoneOffsetMinutes(zone, now), label: zoneLabel(zone, now)}))
    .sort((left, right) =>
      (left.offset ?? Infinity) - (right.offset ?? Infinity)
      || cityOf(left.id).localeCompare(cityOf(right.id), 'en')
      || left.id.localeCompare(right.id, 'en'))
    .map(({id, label}) => ({id, label}));
}
