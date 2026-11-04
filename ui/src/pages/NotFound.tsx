import { Tapir } from "../components/Logo";

// NotFound is the 404 page of tapyr.eu, for unknown pages, jobs and
// captures.
export function NotFound({ what }: { what?: string }) {
  return (
    <div className="lost">
      <Tapir title="A tapir sniffing" />
      <h1>This page is off the trail</h1>
      <p>{what ?? "Tapyr sniffed around but found nothing at this address."}</p>
      <a className="btn-primary" href="#/">Back to the overview</a>
    </div>
  );
}
