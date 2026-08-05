import { Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { allCommands, commandGroups } from './data/groups';

import { Home } from './pages/Home';
import { Quickstart } from './pages/Quickstart';
import { ReferenceIndex } from './pages/ReferenceIndex';
import { CommandGroupPage } from './pages/CommandGroupPage';
import { CommandPage } from './pages/CommandPage';
import { GlobalFlags } from './pages/GlobalFlags';
import { ExitCodesPage } from './pages/ExitCodesPage';
import { JsonEnvelope } from './pages/JsonEnvelope';
import { ConfigReference } from './pages/ConfigReference';
import { ValidationPage } from './pages/ValidationPage';
import { NotFound } from './pages/NotFound';
import { Releases } from './pages/Releases';
import { TopicsIndex } from './pages/TopicsIndex';
import { TopicPage } from './pages/TopicPage';

import { ConceptModel } from './pages/concepts/Model';
import { ConceptSshScopes } from './pages/concepts/SshScopes';
import { ConceptTlsLifecycle } from './pages/concepts/TlsLifecycle';
import { ConceptRateLimits } from './pages/concepts/RateLimits';
import { ConceptTransactions } from './pages/concepts/Transactions';
import { ConceptFilesystem } from './pages/concepts/Filesystem';
import { ConceptSupervision } from './pages/concepts/Supervision';
import { ConceptSecurity } from './pages/concepts/Security';
import { ConceptInteractive } from './pages/concepts/Interactive';

import { GuideFastApi } from './pages/guides/FastApi';
import { GuideNextJs } from './pages/guides/NextJs';
import { GuideAstro } from './pages/guides/Astro';
import { GuideContractorAccess } from './pages/guides/ContractorAccess';
import { GuideNewLaptopKey } from './pages/guides/NewLaptopKey';
import { GuideCiDeployKeys } from './pages/guides/CiDeployKeys';
import { GuideIssueCert } from './pages/guides/IssueCert';
import { GuideCloudflare } from './pages/guides/Cloudflare';
import { GuideRenewalRunbook } from './pages/guides/RenewalRunbook';
import { GuideSshLockout } from './pages/guides/SshLockout';
import { GuideDebug502 } from './pages/guides/Debug502';
import { GuideNodePm2 } from './pages/guides/NodePm2';
import { GuideInheritedServer } from './pages/guides/InheritedServer';
import { GuideMongoDatabase } from './pages/guides/MongoDatabase';

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
