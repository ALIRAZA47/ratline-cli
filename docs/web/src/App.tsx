import { lazy } from 'react';
import { Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { allCommands, commandGroups } from './data/groups';

import { Home } from './pages/Home';
import { NotFound } from './pages/NotFound';

/**
 * Every page but the entry and the 404 is loaded on demand.
 *
 * The site had grown to one 841 kB chunk: a reader arriving to look up a single flag
 * downloaded all 86 command pages, 13 topics and 14 guides first. Pages are 368 kB of the
 * source on their own, and none of it is needed until somebody navigates there.
 *
 * Home stays eager because it is the entry — making it lazy buys a round trip before first
 * paint on the most common arrival. NotFound stays eager because fetching a chunk to
 * discover a page does not exist is the wrong way round.
 */
const Quickstart = lazy(() => import('./pages/Quickstart').then((m) => ({ default: m.Quickstart })));
const ReferenceIndex = lazy(() => import('./pages/ReferenceIndex').then((m) => ({ default: m.ReferenceIndex })));
const CommandGroupPage = lazy(() => import('./pages/CommandGroupPage').then((m) => ({ default: m.CommandGroupPage })));
const CommandPage = lazy(() => import('./pages/CommandPage').then((m) => ({ default: m.CommandPage })));
const GlobalFlags = lazy(() => import('./pages/GlobalFlags').then((m) => ({ default: m.GlobalFlags })));
const ExitCodesPage = lazy(() => import('./pages/ExitCodesPage').then((m) => ({ default: m.ExitCodesPage })));
const JsonEnvelope = lazy(() => import('./pages/JsonEnvelope').then((m) => ({ default: m.JsonEnvelope })));
const ConfigReference = lazy(() => import('./pages/ConfigReference').then((m) => ({ default: m.ConfigReference })));
const ValidationPage = lazy(() => import('./pages/ValidationPage').then((m) => ({ default: m.ValidationPage })));
const Releases = lazy(() => import('./pages/Releases').then((m) => ({ default: m.Releases })));
const TopicsIndex = lazy(() => import('./pages/TopicsIndex').then((m) => ({ default: m.TopicsIndex })));
const TopicPage = lazy(() => import('./pages/TopicPage').then((m) => ({ default: m.TopicPage })));
const ConceptModel = lazy(() => import('./pages/concepts/Model').then((m) => ({ default: m.ConceptModel })));
const ConceptSshScopes = lazy(() => import('./pages/concepts/SshScopes').then((m) => ({ default: m.ConceptSshScopes })));
const ConceptTlsLifecycle = lazy(() => import('./pages/concepts/TlsLifecycle').then((m) => ({ default: m.ConceptTlsLifecycle })));
const ConceptRateLimits = lazy(() => import('./pages/concepts/RateLimits').then((m) => ({ default: m.ConceptRateLimits })));
const ConceptTransactions = lazy(() => import('./pages/concepts/Transactions').then((m) => ({ default: m.ConceptTransactions })));
const ConceptFilesystem = lazy(() => import('./pages/concepts/Filesystem').then((m) => ({ default: m.ConceptFilesystem })));
const ConceptSupervision = lazy(() => import('./pages/concepts/Supervision').then((m) => ({ default: m.ConceptSupervision })));
const ConceptSecurity = lazy(() => import('./pages/concepts/Security').then((m) => ({ default: m.ConceptSecurity })));
const ConceptInteractive = lazy(() => import('./pages/concepts/Interactive').then((m) => ({ default: m.ConceptInteractive })));
const GuideFastApi = lazy(() => import('./pages/guides/FastApi').then((m) => ({ default: m.GuideFastApi })));
const GuideNextJs = lazy(() => import('./pages/guides/NextJs').then((m) => ({ default: m.GuideNextJs })));
const GuideAstro = lazy(() => import('./pages/guides/Astro').then((m) => ({ default: m.GuideAstro })));
const GuideContractorAccess = lazy(() => import('./pages/guides/ContractorAccess').then((m) => ({ default: m.GuideContractorAccess })));
const GuideNewLaptopKey = lazy(() => import('./pages/guides/NewLaptopKey').then((m) => ({ default: m.GuideNewLaptopKey })));
const GuideCiDeployKeys = lazy(() => import('./pages/guides/CiDeployKeys').then((m) => ({ default: m.GuideCiDeployKeys })));
const GuideIssueCert = lazy(() => import('./pages/guides/IssueCert').then((m) => ({ default: m.GuideIssueCert })));
const GuideCloudflare = lazy(() => import('./pages/guides/Cloudflare').then((m) => ({ default: m.GuideCloudflare })));
const GuideRenewalRunbook = lazy(() => import('./pages/guides/RenewalRunbook').then((m) => ({ default: m.GuideRenewalRunbook })));
const GuideSshLockout = lazy(() => import('./pages/guides/SshLockout').then((m) => ({ default: m.GuideSshLockout })));
const GuideDebug502 = lazy(() => import('./pages/guides/Debug502').then((m) => ({ default: m.GuideDebug502 })));
const GuideNodePm2 = lazy(() => import('./pages/guides/NodePm2').then((m) => ({ default: m.GuideNodePm2 })));
const GuideInheritedServer = lazy(() => import('./pages/guides/InheritedServer').then((m) => ({ default: m.GuideInheritedServer })));
const GuideMongoDatabase = lazy(() => import('./pages/guides/MongoDatabase').then((m) => ({ default: m.GuideMongoDatabase })));

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Home />} />
        <Route path="quickstart" element={<Quickstart />} />

        <Route path="releases" element={<Releases />} />
        <Route path="topics" element={<TopicsIndex />} />
        {/* One route for every topic the binary carries, so a topic added there
            appears here without a second list to keep in step. */}
        <Route path="topics/:name" element={<TopicPage />} />
        <Route path="reference" element={<ReferenceIndex />} />
        <Route path="reference/global-flags" element={<GlobalFlags />} />
        <Route path="reference/exit-codes" element={<ExitCodesPage />} />
        <Route path="reference/json" element={<JsonEnvelope />} />
        <Route path="reference/validation" element={<ValidationPage />} />
        <Route path="reference/config" element={<ConfigReference />} />
        {/* One index page per group, and one page per command — 8 and 86 routes, all
            generated from the same typed data, so a command added to the data is a page. */}
        {commandGroups.map((g) => (
          <Route
            key={g.id}
            path={g.path.replace(/^\//, '')}
            element={<CommandGroupPage />}
          />
        ))}
        {allCommands.map((c) => (
          <Route key={c.path} path={c.path.replace(/^\//, '')} element={<CommandPage />} />
        ))}

        <Route path="concepts/model" element={<ConceptModel />} />
        <Route path="concepts/ssh-scopes" element={<ConceptSshScopes />} />
        <Route path="concepts/tls-lifecycle" element={<ConceptTlsLifecycle />} />
        <Route path="concepts/rate-limits" element={<ConceptRateLimits />} />
        <Route path="concepts/transactions" element={<ConceptTransactions />} />
        <Route path="concepts/filesystem" element={<ConceptFilesystem />} />
        <Route path="concepts/supervision" element={<ConceptSupervision />} />
        <Route path="concepts/security" element={<ConceptSecurity />} />
        <Route path="concepts/interactive" element={<ConceptInteractive />} />

        <Route path="guides/fastapi" element={<GuideFastApi />} />
        <Route path="guides/nextjs" element={<GuideNextJs />} />
        <Route path="guides/astro" element={<GuideAstro />} />
        <Route path="guides/contractor-access" element={<GuideContractorAccess />} />
        <Route path="guides/new-laptop-key" element={<GuideNewLaptopKey />} />
        <Route path="guides/ci-deploy-keys" element={<GuideCiDeployKeys />} />
        <Route path="guides/issue-cert" element={<GuideIssueCert />} />
        <Route path="guides/cloudflare" element={<GuideCloudflare />} />
        <Route path="guides/renewal-runbook" element={<GuideRenewalRunbook />} />
        <Route path="guides/ssh-lockout" element={<GuideSshLockout />} />
        <Route path="guides/debug-502" element={<GuideDebug502 />} />
        <Route path="guides/node" element={<GuideNodePm2 />} />
        <Route path="guides/inherited-server" element={<GuideInheritedServer />} />
        <Route path="guides/mongodb" element={<GuideMongoDatabase />} />

        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
