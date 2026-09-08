CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE schema_migrations (
  filename text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email           citext NOT NULL,
  email_verified_at timestamptz,
  password_hash   text,
  display_name    text NOT NULL,
  dob             date NOT NULL,
  is_admin        boolean NOT NULL DEFAULT false,
  locale          text NOT NULL DEFAULT 'en-US',
  units           text NOT NULL DEFAULT 'imperial' CHECK (units IN ('imperial','metric')),
  tz              text NOT NULL DEFAULT 'America/Denver',
  calorie_goal    integer,
  protein_goal_g  integer,
  ai_consent_at   timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz
);
CREATE UNIQUE INDEX users_email_live ON users (email) WHERE deleted_at IS NULL;

CREATE TABLE user_identities (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          uuid NOT NULL REFERENCES users(id),
  provider         text NOT NULL CHECK (provider IN ('google','apple')),
  provider_subject text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_subject)
);

CREATE TABLE sessions (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id),
  kind       text NOT NULL CHECK (kind IN ('cookie','bearer')),
  token_hash bytea NOT NULL UNIQUE,
  user_agent text,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE refresh_tokens (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id),
  token_hash bytea NOT NULL UNIQUE,
  family_id  uuid NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE magic_links (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email      citext NOT NULL,
  token_hash bytea NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);

CREATE TABLE api_tokens (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id),
  name         text NOT NULL,
  token_hash   bytea NOT NULL UNIQUE,
  prefix       text NOT NULL,
  scopes       text[] NOT NULL DEFAULT ARRAY['mcp'],
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at   timestamptz
);

CREATE TABLE media_objects (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id),
  sha256       bytea NOT NULL,
  content_type text NOT NULL CHECK (content_type IN ('image/jpeg','image/png')),
  bytes        integer NOT NULL,
  path         text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX media_sha_user ON media_objects (user_id, sha256);

CREATE TABLE profiles (
  user_id         uuid PRIMARY KEY REFERENCES users(id),
  bio             text,
  avatar_media_id uuid REFERENCES media_objects(id),
  height_cm       numeric
);

CREATE TABLE circles (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name       text NOT NULL,
  emoji      text,
  tz         text NOT NULL DEFAULT 'America/Denver',
  created_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);

CREATE TABLE circle_members (
  circle_id uuid NOT NULL REFERENCES circles(id),
  user_id   uuid NOT NULL REFERENCES users(id),
  role      text NOT NULL CHECK (role IN ('owner','admin','member')),
  joined_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (circle_id, user_id)
);

CREATE TABLE invites (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  circle_id  uuid NOT NULL REFERENCES circles(id),
  token_hash bytea NOT NULL UNIQUE,
  created_by uuid NOT NULL REFERENCES users(id),
  expires_at timestamptz NOT NULL,
  max_uses   integer CHECK (max_uses IS NULL OR max_uses > 0),
  uses       integer NOT NULL DEFAULT 0
);

CREATE TABLE rituals (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id uuid REFERENCES users(id),
  circle_id     uuid REFERENCES circles(id),
  type          text NOT NULL CHECK (type IN ('weight','workout','habit','fishing','meal','custom')),
  title         text NOT NULL,
  target_value  numeric,
  target_unit   text,
  direction     text NOT NULL DEFAULT 'at_least'
                  CHECK (direction IN ('at_least','at_most','hit')),
  period        text NOT NULL DEFAULT 'none'
                  CHECK (period IN ('none','daily','weekly','season','date_range')),
  scoring_key   text NOT NULL DEFAULT ''
                  CHECK (scoring_key IN (
                    '', 'habit.completion', 'fishing.days', 'fishing.catches',
                    'weight.progress', 'workout.volume', 'custom.sum', 'custom.average'
                  )),
  created_at    timestamptz NOT NULL DEFAULT now(),
  deleted_at    timestamptz,
  CHECK ((owner_user_id IS NOT NULL) OR (circle_id IS NOT NULL))
);

CREATE TABLE challenges (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  circle_id     uuid NOT NULL REFERENCES circles(id),
  ritual_id     uuid REFERENCES rituals(id),
  type          text NOT NULL CHECK (type IN ('weight','workout','habit','fishing','custom')),
  scoring_key   text NOT NULL CHECK (scoring_key IN (
                    'habit.completion', 'fishing.days', 'fishing.catches',
                    'weight.progress', 'workout.volume', 'custom.sum', 'custom.average'
                  )),
  direction     text CHECK (direction IS NULL OR direction IN ('at_most','at_least')),
  name          text NOT NULL,
  starts_at     timestamptz NOT NULL,
  ends_at       timestamptz NOT NULL,
  require_photo boolean NOT NULL DEFAULT false,
  join_policy   text NOT NULL DEFAULT 'opt_in' CHECK (join_policy = 'opt_in'),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at),
  CHECK (
    (scoring_key = 'weight.progress' AND direction IN ('at_most','at_least'))
    OR (scoring_key <> 'weight.progress' AND direction IS NULL)
  )
);

CREATE TABLE challenge_participants (
  challenge_id        uuid NOT NULL REFERENCES challenges(id),
  user_id             uuid NOT NULL REFERENCES users(id),
  share_matching_logs boolean NOT NULL,
  joined_at           timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (challenge_id, user_id)
);

CREATE TABLE logs (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id),
  ritual_id    uuid REFERENCES rituals(id),
  challenge_id uuid REFERENCES challenges(id),
  type         text NOT NULL CHECK (type IN ('weight','workout','habit','fishing','meal','custom')),
  logged_at    timestamptz NOT NULL,
  visibility   text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','circle','challenge')),
  notes        text,
  media_id     uuid REFERENCES media_objects(id),
  source       text NOT NULL DEFAULT 'app'
                 CHECK (source IN ('app','ai_chat','ai_vision','mcp','intent','share')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  deleted_at   timestamptz
);
CREATE INDEX logs_user_logged ON logs (user_id, logged_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE log_circles (
  log_id    uuid NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
  circle_id uuid NOT NULL REFERENCES circles(id),
  PRIMARY KEY (log_id, circle_id)
);

CREATE TABLE weight_logs (
  log_id uuid PRIMARY KEY REFERENCES logs(id) ON DELETE CASCADE,
  kg     numeric NOT NULL CHECK (kg > 0 AND kg < 500)
);

CREATE TABLE workout_logs (
  log_id uuid PRIMARY KEY REFERENCES logs(id) ON DELETE CASCADE,
  title  text NOT NULL
);

CREATE TABLE workout_sets (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workout_log_id uuid NOT NULL REFERENCES workout_logs(log_id) ON DELETE CASCADE,
  exercise       text NOT NULL,
  reps           integer CHECK (reps IS NULL OR reps >= 0),
  weight_kg      numeric CHECK (weight_kg IS NULL OR weight_kg >= 0),
  rpe            numeric,
  ordinal        integer NOT NULL
);

CREATE TABLE habit_logs (
  log_id uuid PRIMARY KEY REFERENCES logs(id) ON DELETE CASCADE,
  status text NOT NULL CHECK (status IN ('done','skip'))
);

CREATE TABLE fishing_logs (
  log_id     uuid PRIMARY KEY REFERENCES logs(id) ON DELETE CASCADE,
  water_body text,
  lat        numeric CHECK (lat IS NULL OR (lat >= -90 AND lat <= 90)),
  lng        numeric CHECK (lng IS NULL OR (lng >= -180 AND lng <= 180)),
  started_at timestamptz,
  ended_at   timestamptz
);

CREATE TABLE fishing_catches (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  fishing_log_id uuid NOT NULL REFERENCES fishing_logs(log_id) ON DELETE CASCADE,
  species        text,
  count          integer NOT NULL DEFAULT 1 CHECK (count >= 0),
  length_cm      numeric,
  released       boolean NOT NULL DEFAULT true
);

CREATE TABLE custom_logs (
  log_id uuid PRIMARY KEY REFERENCES logs(id) ON DELETE CASCADE,
  value  numeric NOT NULL,
  unit   text NOT NULL DEFAULT ''
);

CREATE TABLE meals (
  log_id         uuid PRIMARY KEY REFERENCES logs(id) ON DELETE CASCADE,
  status         text NOT NULL CHECK (status IN ('draft','confirmed')),
  kcal           numeric NOT NULL DEFAULT 0,
  protein_g      numeric NOT NULL DEFAULT 0,
  carbs_g        numeric NOT NULL DEFAULT 0,
  fat_g          numeric NOT NULL DEFAULT 0,
  confidence     numeric,
  photo_media_id uuid REFERENCES media_objects(id)
);

CREATE TABLE meal_items (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  meal_id   uuid NOT NULL REFERENCES meals(log_id) ON DELETE CASCADE,
  name      text NOT NULL CHECK (char_length(name) <= 80),
  grams     numeric,
  kcal      numeric NOT NULL DEFAULT 0,
  protein_g numeric NOT NULL DEFAULT 0,
  carbs_g   numeric NOT NULL DEFAULT 0,
  fat_g     numeric NOT NULL DEFAULT 0,
  source    text NOT NULL DEFAULT 'user' CHECK (source IN ('vision','user','ai_text'))
);

CREATE TABLE vision_cache (
  sha256     bytea PRIMARY KEY,
  result     jsonb NOT NULL,
  model      text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE feed_posts (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  circle_id    uuid NOT NULL REFERENCES circles(id),
  user_id      uuid REFERENCES users(id),
  log_id       uuid REFERENCES logs(id),
  challenge_id uuid REFERENCES challenges(id),
  body         text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  deleted_at   timestamptz
);

CREATE TABLE comments (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  post_id    uuid NOT NULL REFERENCES feed_posts(id),
  user_id    uuid REFERENCES users(id),
  body       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);

CREATE TABLE reactions (
  post_id uuid NOT NULL REFERENCES feed_posts(id),
  user_id uuid NOT NULL REFERENCES users(id),
  emoji   text NOT NULL CHECK (emoji IN ('like','fire','fish','strong','heart')),
  PRIMARY KEY (post_id, user_id)
);

CREATE TABLE ai_conversations (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ai_messages (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id uuid NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
  role            text NOT NULL CHECK (role IN ('user','assistant','tool')),
  content         text,
  tool_name       text,
  tokens_in       integer,
  tokens_out      integer,
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind       text NOT NULL CHECK (kind IN ('recap','media_gc','vision_cache_gc','email')),
  payload    jsonb NOT NULL DEFAULT '{}',
  run_at     timestamptz NOT NULL DEFAULT now(),
  locked_at  timestamptz,
  locked_by  text,
  attempts   integer NOT NULL DEFAULT 0,
  last_error text,
  done_at    timestamptz
);

CREATE TABLE audit_log (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid,
  action     text NOT NULL,
  meta       jsonb NOT NULL DEFAULT '{}',
  ip         inet,
  created_at timestamptz NOT NULL DEFAULT now()
);
