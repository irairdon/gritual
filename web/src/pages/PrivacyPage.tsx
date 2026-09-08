export default function PrivacyPage() {
  return (
    <article className="prose-sm max-w-none space-y-4 text-stone-800">
      <h1 className="text-2xl font-semibold">Privacy</h1>
      <p>
        Gritual is a consumer wellness product. It is <strong>not</strong> a HIPAA covered entity and we do not claim HIPAA
        compliance. Logs, photos, and chat are sensitive personal data. We treat them carefully, but this is not a medical
        record and Gritual is not treatment, diagnosis, or billing.
      </p>
      <p>
        Meal photos you upload later may be sent to SpaceXAI (xAI) so we can estimate food. Gritual cannot delete copies
        xAI may retain. Do not upload photos of other people without their OK. You will be asked to consent in-app before
        any photo or coach chat is sent.
      </p>
      <p>
        Account deletion removes data we store (database rows and files on this host). It cannot erase third-party copies
        already processed by xAI.
      </p>
    </article>
  );
}
