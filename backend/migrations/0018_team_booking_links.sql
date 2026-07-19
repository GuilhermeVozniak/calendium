-- 0018_team_booking_links.sql — team booking links (M2.7 Task 14).
-- booking_links gains an optional team scope; booking_link_members lists the
-- team members whose free/busy is intersected into the link's COLLECTIVE
-- availability and who are invited on every confirmed booking. Round-robin
-- rotation (single rotating host per booking) is future work and would add
-- per-link rotation state here.

ALTER TABLE booking_links
    ADD COLUMN team_id text REFERENCES teams(id) ON DELETE CASCADE;
CREATE INDEX booking_links_team_idx ON booking_links (team_id) WHERE team_id IS NOT NULL;

CREATE TABLE booking_link_members (
    booking_link_id text NOT NULL REFERENCES booking_links(id) ON DELETE CASCADE,
    user_id         text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (booking_link_id, user_id)
);
CREATE INDEX booking_link_members_user_idx ON booking_link_members (user_id);
