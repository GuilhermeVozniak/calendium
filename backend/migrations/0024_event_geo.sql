-- M2.8 Task 11: optional coordinates on events, populated when the user picks
-- a location-autocomplete suggestion (Nominatim). NULL means the location was
-- free-typed text with no geo data — travel features simply skip such events.
ALTER TABLE events
    ADD COLUMN location_lat DOUBLE PRECISION,
    ADD COLUMN location_lon DOUBLE PRECISION;
