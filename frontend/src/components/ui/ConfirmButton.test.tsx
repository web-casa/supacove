import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Trash2 } from "lucide-react";
import { I18nProvider } from "../../i18n";
import { ConfirmButton } from "./ConfirmButton";

function renderWithI18n(onConfirm: () => void) {
  return render(
    <I18nProvider>
      <ConfirmButton icon={Trash2} tip="Remove" prompt={'Remove "prod"?'} confirmLabel="Remove" onConfirm={onConfirm} />
    </I18nProvider>,
  );
}

describe("ConfirmButton two-step destructive action", () => {
  it("first click only arms: no confirm callback", () => {
    const onConfirm = vi.fn();
    renderWithI18n(onConfirm);
    fireEvent.click(screen.getByRole("button"));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.getByText('Remove "prod"?')).toBeInTheDocument();
  });

  it("cancel disarms without confirming", () => {
    const onConfirm = vi.fn();
    renderWithI18n(onConfirm);
    fireEvent.click(screen.getByRole("button"));
    fireEvent.click(screen.getByText("Cancel"));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.queryByText('Remove "prod"?')).not.toBeInTheDocument();
  });

  it("Escape disarms the armed state", () => {
    const onConfirm = vi.fn();
    const { container } = renderWithI18n(onConfirm);
    fireEvent.click(screen.getByRole("button"));
    fireEvent.keyDown(container.querySelector('[role="group"]')!, { key: "Escape" });
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.queryByText('Remove "prod"?')).not.toBeInTheDocument();
  });

  it("second click confirms exactly once", () => {
    const onConfirm = vi.fn();
    renderWithI18n(onConfirm);
    fireEvent.click(screen.getByRole("button"));
    fireEvent.click(screen.getByText("Remove"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
