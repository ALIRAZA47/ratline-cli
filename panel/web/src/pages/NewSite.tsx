import { useNavigate } from 'react-router-dom';
import { Page } from '../components/Layout';
import { ActionForm, type FormStep } from '../components/ActionForm';
import { useApi } from '../lib/hooks';
import type { Action } from '../lib/types';
import { Card, ErrorBox, Spinner } from '../components/ui';

/**
 * Putting a site up, asked four questions at a time.
 *
 * `site add` takes around forty flags. Offered all at once — which is what the
 * catalogue hands over and what every other action page shows — that is a wall, and
 * the people this panel exists for close it. So the same generated form is paged:
 * the address, then what it runs, then who owns it, then the whole thing shown back
 * before anything happens.
 *
 * The grouping below is the only part written down, and deliberately so: it is a
 * judgement about what to ask first, which is not a fact the binary can tell us. It
 * is also not a list of the fields — anything `site add` has that is not named here
 * still appears, in a step of its own before the review. A ratline release that adds
 * a flag cannot go missing from this page.
 */
const STEPS: FormStep[] = [
  {
    title: 'Its address',
    heading: 'What address will people visit?',
    lede: 'The domain or subdomain that should serve this site. Point its DNS at this server before you ask for a certificate — not before you create the site.',
    fields: ['domain', 'alias', 'aliases'],
  },
  {
    title: 'What it runs',
    heading: 'What does this site run?',
    lede: 'If you are not sure, pick the one that matches how you start it on your own machine.',
    // Ordered as they are asked, not alphabetically: the runtime is the question,
    // and everything after it only makes sense once it has been answered.
    fields: [
      'runtime',
      'node',
      'python',
      'bun',
      'entry',
      'app-module',
      'start',
      'workers',
      'instances',
      'daemon',
      'listen',
      'port',
      'root',
      'public',
    ],
  },
  {
    title: 'Who owns it',
    heading: 'Which server user owns it?',
    lede: 'A site lives inside one user’s home and runs as that user. If the application is ever compromised, this is the boundary that holds — so a site that has nothing to do with your others should have an owner of its own.',
    fields: ['user', 'owner', 'repo', 'branch'],
  },
];

export function NewSite() {
  const navigate = useNavigate();
  const { data, error, loading } = useApi<Action>('/api/actions/site.add');

  return (
    <Page
      title="New site"
      lede="Four questions, then you see the command before it runs."
      back={{ to: '/sites', label: 'Sites' }}
    >
      <ErrorBox error={error} title="Creating a site is not available to you" />
      {loading && !data && <Spinner label="Asking ratline what it needs" />}
      {data && (
        <Card>
          <ActionForm
            action={data}
            steps={STEPS}
            onCancel={() => navigate('/sites')}
            onDone={(res) => {
              // A site add that ran as a job has a transcript worth watching; one
              // that finished in the request has a page worth landing on.
              if (!res.ok) return;
              if (res.job_id) navigate(`/jobs/${res.job_id}`);
              else navigate('/sites');
            }}
          />
        </Card>
      )}
    </Page>
  );
}
