import { HomePage, homeMetadata } from "@/routes/home-page";

export const metadata = homeMetadata("en");

export default function Page() {
  return <HomePage lang="en" />;
}
