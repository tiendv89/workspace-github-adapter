-- Consolidated schema snapshot, vendored from workflow-backend so sqlc is not
-- coupled to the sibling repo's migration files at generate time. This is the
-- full DDL after all applied migrations (pg_dump --schema-only), not a
-- migration history.
--
-- Refresh deliberately when workflow-backend's schema moves forward:
--   1. Apply workflow-backend's migrations to a scratch Postgres (e.g. via
--      `goose -dir <path-to-migrations> postgres "<dsn>" up`).
--   2. `pg_dump --schema-only --no-owner --no-privileges -T goose_db_version`
--      against that database, replacing this file's body below.
--   3. Update database/queries/*.sql for any new/changed columns, then
--      `make sqlc` and fix up internal/adapter/db as needed.


--
-- Name: models; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.models (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    model_id text NOT NULL,
    display_name text,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: workspace_activity_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_activity_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    scope_type text NOT NULL,
    action text,
    actor text,
    occurred_at text,
    note text,
    sequence integer NOT NULL,
    raw_event jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    feature_name text,
    task_name text,
    feature_id uuid,
    task_id uuid,
    actor_id text,
    enriched boolean DEFAULT true NOT NULL
);

--
-- Name: workspace_feature_documents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_feature_documents (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    document_type text NOT NULL,
    source_path text NOT NULL,
    url text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    feature_name text NOT NULL,
    feature_id uuid NOT NULL
);

--
-- Name: workspace_feature_handoff_prs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_feature_handoff_prs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    handoff_id uuid NOT NULL,
    repo text NOT NULL,
    pr_url text,
    status text DEFAULT 'open'::text NOT NULL,
    conflict_state text DEFAULT 'none'::text NOT NULL,
    conflict_resolution_attempts integer DEFAULT 0 NOT NULL,
    dispatch_handle text,
    dispatch_nonce text,
    dispatched_at timestamp with time zone,
    reenqueue_attempts integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: workspace_feature_handoffs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_feature_handoffs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    feature_id uuid NOT NULL,
    mgmt_pr_url text,
    status text DEFAULT 'draft'::text NOT NULL,
    create_attempts integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    finalized_at timestamp with time zone
);

--
-- Name: workspace_features; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_features (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    title text NOT NULL,
    feature_status text,
    current_stage text,
    next_action text,
    stages jsonb,
    source_path text,
    source_hash text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    feature_name text NOT NULL,
    feature_id uuid DEFAULT gen_random_uuid() NOT NULL,
    owner text,
    init_pr_url text,
    init_pr_merged boolean DEFAULT false NOT NULL
);

--
-- Name: workspace_github_sources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_github_sources (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    repo_url text NOT NULL,
    repo_owner text NOT NULL,
    repo_name text NOT NULL,
    default_branch text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: workspace_model_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_model_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    phase text NOT NULL,
    model_id uuid NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: workspace_repos; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_repos (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    repo_id text NOT NULL,
    base_branch text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    repo_url text
);

--
-- Name: workspace_sync_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_sync_runs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    trigger text NOT NULL,
    branch text,
    mode text NOT NULL,
    status text DEFAULT 'running'::text NOT NULL,
    commit_sha text,
    changed_paths jsonb,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    error_code text,
    error_message text,
    metadata jsonb,
    feature_id uuid,
    task_id uuid
);

--
-- Name: workspace_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspace_tasks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    workspace_id uuid NOT NULL,
    title text NOT NULL,
    repo text,
    status text,
    depends_on jsonb DEFAULT '[]'::jsonb NOT NULL,
    blocked_reason text,
    branch text,
    execution jsonb,
    pr jsonb,
    workspace_pr jsonb,
    source_path text,
    source_hash text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    feature_name text NOT NULL,
    feature_id uuid NOT NULL,
    task_name text NOT NULL,
    task_id uuid DEFAULT gen_random_uuid() NOT NULL,
    owner text,
    dispatch_handle text,
    dispatch_nonce text,
    dispatched_at timestamp with time zone,
    reenqueue_attempts integer DEFAULT 0 NOT NULL,
    review_incomplete_count integer DEFAULT 0 NOT NULL,
    max_turns_retry_count integer DEFAULT 0 NOT NULL,
    rebase_attempts integer DEFAULT 0 NOT NULL,
    conflict_state text DEFAULT 'none'::text NOT NULL,
    dispatch_kind text,
    blocked_from_status text,
    blocked_details text
);

--
-- Name: workspaces; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workspaces (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    management_repo_id text NOT NULL,
    branch_pattern text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    slack_channel_id text,
    organization_id uuid NOT NULL
);

--
-- Name: models models_model_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.models
    ADD CONSTRAINT models_model_id_key UNIQUE (model_id);

--
-- Name: models models_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.models
    ADD CONSTRAINT models_pkey PRIMARY KEY (id);

--
-- Name: workspace_activity_events workspace_activity_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_activity_events
    ADD CONSTRAINT workspace_activity_events_pkey PRIMARY KEY (id);

--
-- Name: workspace_feature_documents workspace_feature_documents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_documents
    ADD CONSTRAINT workspace_feature_documents_pkey PRIMARY KEY (id);

--
-- Name: workspace_feature_documents workspace_feature_documents_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_documents
    ADD CONSTRAINT workspace_feature_documents_unique UNIQUE (workspace_id, feature_id, document_type);

--
-- Name: workspace_feature_handoff_prs workspace_feature_handoff_prs_handoff_repo_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_handoff_prs
    ADD CONSTRAINT workspace_feature_handoff_prs_handoff_repo_unique UNIQUE (handoff_id, repo);

--
-- Name: workspace_feature_handoff_prs workspace_feature_handoff_prs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_handoff_prs
    ADD CONSTRAINT workspace_feature_handoff_prs_pkey PRIMARY KEY (id);

--
-- Name: workspace_feature_handoffs workspace_feature_handoffs_feature_id_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_handoffs
    ADD CONSTRAINT workspace_feature_handoffs_feature_id_unique UNIQUE (feature_id);

--
-- Name: workspace_feature_handoffs workspace_feature_handoffs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_handoffs
    ADD CONSTRAINT workspace_feature_handoffs_pkey PRIMARY KEY (id);

--
-- Name: workspace_features workspace_features_feature_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_features
    ADD CONSTRAINT workspace_features_feature_id_key UNIQUE (feature_id);

--
-- Name: workspace_features workspace_features_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_features
    ADD CONSTRAINT workspace_features_pkey PRIMARY KEY (id);

--
-- Name: workspace_features workspace_features_workspace_feature_id_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_features
    ADD CONSTRAINT workspace_features_workspace_feature_id_unique UNIQUE (workspace_id, feature_id);

--
-- Name: workspace_features workspace_features_workspace_feature_name_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_features
    ADD CONSTRAINT workspace_features_workspace_feature_name_unique UNIQUE (workspace_id, feature_name);

--
-- Name: workspace_github_sources workspace_github_sources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_github_sources
    ADD CONSTRAINT workspace_github_sources_pkey PRIMARY KEY (id);

--
-- Name: workspace_github_sources workspace_github_sources_repo_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_github_sources
    ADD CONSTRAINT workspace_github_sources_repo_unique UNIQUE (repo_owner, repo_name);

--
-- Name: workspace_github_sources workspace_github_sources_workspace_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_github_sources
    ADD CONSTRAINT workspace_github_sources_workspace_unique UNIQUE (workspace_id);

--
-- Name: workspace_model_policies workspace_model_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_model_policies
    ADD CONSTRAINT workspace_model_policies_pkey PRIMARY KEY (id);

--
-- Name: workspace_repos workspace_repos_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_repos
    ADD CONSTRAINT workspace_repos_pkey PRIMARY KEY (id);

--
-- Name: workspace_repos workspace_repos_workspace_repo_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_repos
    ADD CONSTRAINT workspace_repos_workspace_repo_unique UNIQUE (workspace_id, repo_id);

--
-- Name: workspace_sync_runs workspace_sync_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_sync_runs
    ADD CONSTRAINT workspace_sync_runs_pkey PRIMARY KEY (id);

--
-- Name: workspace_tasks workspace_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_tasks
    ADD CONSTRAINT workspace_tasks_pkey PRIMARY KEY (id);

--
-- Name: workspace_tasks workspace_tasks_workspace_feature_task_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_feature_task_unique UNIQUE (workspace_id, feature_id, task_name);

--
-- Name: workspace_tasks workspace_tasks_workspace_task_id_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_task_id_unique UNIQUE (workspace_id, task_id);

--
-- Name: workspaces workspaces_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspaces
    ADD CONSTRAINT workspaces_pkey PRIMARY KEY (id);

--
-- Name: workspaces workspaces_slug_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspaces
    ADD CONSTRAINT workspaces_slug_unique UNIQUE (slug);

--
-- Name: idx_workspace_activity_events_unenriched; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_activity_events_unenriched ON public.workspace_activity_events USING btree (id) WHERE (enriched = false);

--
-- Name: idx_workspace_activity_feature; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_activity_feature ON public.workspace_activity_events USING btree (workspace_id, feature_id, occurred_at);

--
-- Name: idx_workspace_activity_feature_seq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_workspace_activity_feature_seq ON public.workspace_activity_events USING btree (workspace_id, feature_id, sequence) WHERE ((feature_id IS NOT NULL) AND (task_id IS NULL));

--
-- Name: idx_workspace_activity_scope; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_activity_scope ON public.workspace_activity_events USING btree (workspace_id, scope_type, occurred_at);

--
-- Name: idx_workspace_activity_task; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_activity_task ON public.workspace_activity_events USING btree (workspace_id, feature_id, task_id, occurred_at);

--
-- Name: idx_workspace_activity_task_seq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_workspace_activity_task_seq ON public.workspace_activity_events USING btree (workspace_id, feature_id, task_id, sequence) WHERE ((feature_id IS NOT NULL) AND (task_id IS NOT NULL));

--
-- Name: idx_workspace_feature_documents_feature; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_feature_documents_feature ON public.workspace_feature_documents USING btree (workspace_id, feature_id);

--
-- Name: idx_workspace_feature_handoff_prs_handoff_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_feature_handoff_prs_handoff_id ON public.workspace_feature_handoff_prs USING btree (handoff_id);

--
-- Name: idx_workspace_feature_handoff_prs_resolving; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_feature_handoff_prs_resolving ON public.workspace_feature_handoff_prs USING btree (id) WHERE (conflict_state = 'resolving'::text);

--
-- Name: idx_workspace_features_stage; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_features_stage ON public.workspace_features USING btree (workspace_id, current_stage);

--
-- Name: idx_workspace_features_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_features_status ON public.workspace_features USING btree (workspace_id, feature_status);

--
-- Name: idx_workspace_sync_runs_feature; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_sync_runs_feature ON public.workspace_sync_runs USING btree (workspace_id, feature_id);

--
-- Name: idx_workspace_sync_runs_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_sync_runs_status ON public.workspace_sync_runs USING btree (workspace_id, status);

--
-- Name: idx_workspace_sync_runs_task; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_sync_runs_task ON public.workspace_sync_runs USING btree (workspace_id, task_id);

--
-- Name: idx_workspace_sync_runs_trigger; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_sync_runs_trigger ON public.workspace_sync_runs USING btree (workspace_id, trigger);

--
-- Name: idx_workspace_sync_runs_workspace_started; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_sync_runs_workspace_started ON public.workspace_sync_runs USING btree (workspace_id, started_at DESC);

--
-- Name: idx_workspace_tasks_feature; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_tasks_feature ON public.workspace_tasks USING btree (workspace_id, feature_id);

--
-- Name: idx_workspace_tasks_go_inflight; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_tasks_go_inflight ON public.workspace_tasks USING btree (workspace_id) WHERE ((owner = 'go'::text) AND ((status = ANY (ARRAY['in_progress'::text, 'reviewing'::text])) OR (conflict_state = 'resolving'::text)));

--
-- Name: idx_workspace_tasks_repo; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_tasks_repo ON public.workspace_tasks USING btree (workspace_id, repo);

--
-- Name: idx_workspace_tasks_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspace_tasks_status ON public.workspace_tasks USING btree (workspace_id, status);

--
-- Name: idx_workspaces_organization_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspaces_organization_id ON public.workspaces USING btree (organization_id);

--
-- Name: idx_workspaces_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workspaces_updated_at ON public.workspaces USING btree (updated_at);

--
-- Name: workspace_features_owner_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX workspace_features_owner_idx ON public.workspace_features USING btree (workspace_id, owner);

--
-- Name: workspace_model_policies_workspace_id_phase_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX workspace_model_policies_workspace_id_phase_idx ON public.workspace_model_policies USING btree (workspace_id, phase) WHERE (is_default = true);

--
-- Name: workspace_model_policies_workspace_id_phase_model_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX workspace_model_policies_workspace_id_phase_model_id_idx ON public.workspace_model_policies USING btree (workspace_id, phase, model_id);

--
-- Name: workspace_tasks_owner_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX workspace_tasks_owner_status_idx ON public.workspace_tasks USING btree (workspace_id, owner, status);

--
-- Name: workspace_activity_events workspace_activity_events_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_activity_events
    ADD CONSTRAINT workspace_activity_events_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_feature_documents workspace_feature_documents_feature_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_documents
    ADD CONSTRAINT workspace_feature_documents_feature_id_fkey FOREIGN KEY (feature_id) REFERENCES public.workspace_features(feature_id) ON DELETE CASCADE;

--
-- Name: workspace_feature_documents workspace_feature_documents_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_documents
    ADD CONSTRAINT workspace_feature_documents_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_feature_handoff_prs workspace_feature_handoff_prs_handoff_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_handoff_prs
    ADD CONSTRAINT workspace_feature_handoff_prs_handoff_id_fkey FOREIGN KEY (handoff_id) REFERENCES public.workspace_feature_handoffs(id) ON DELETE CASCADE;

--
-- Name: workspace_feature_handoffs workspace_feature_handoffs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_feature_handoffs
    ADD CONSTRAINT workspace_feature_handoffs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_features workspace_features_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_features
    ADD CONSTRAINT workspace_features_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_github_sources workspace_github_sources_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_github_sources
    ADD CONSTRAINT workspace_github_sources_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_model_policies workspace_model_policies_model_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_model_policies
    ADD CONSTRAINT workspace_model_policies_model_id_fkey FOREIGN KEY (model_id) REFERENCES public.models(id);

--
-- Name: workspace_model_policies workspace_model_policies_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_model_policies
    ADD CONSTRAINT workspace_model_policies_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_repos workspace_repos_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_repos
    ADD CONSTRAINT workspace_repos_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_sync_runs workspace_sync_runs_feature_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_sync_runs
    ADD CONSTRAINT workspace_sync_runs_feature_id_fkey FOREIGN KEY (feature_id) REFERENCES public.workspace_features(id) ON DELETE SET NULL;

--
-- Name: workspace_sync_runs workspace_sync_runs_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_sync_runs
    ADD CONSTRAINT workspace_sync_runs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.workspace_tasks(id) ON DELETE SET NULL;

--
-- Name: workspace_sync_runs workspace_sync_runs_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_sync_runs
    ADD CONSTRAINT workspace_sync_runs_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

--
-- Name: workspace_tasks workspace_tasks_feature_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_tasks
    ADD CONSTRAINT workspace_tasks_feature_id_fkey FOREIGN KEY (feature_id) REFERENCES public.workspace_features(feature_id);

--
-- Name: workspace_tasks workspace_tasks_workspace_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workspace_tasks
    ADD CONSTRAINT workspace_tasks_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES public.workspaces(id) ON DELETE CASCADE;

